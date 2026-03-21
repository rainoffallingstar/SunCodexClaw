package feishunative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type audioTranscript struct {
	Text             string
	Model            string
	Converted        bool
	PreparedFilePath string
}

type preparedAudio struct {
	FilePath  string
	MIMEType  string
	Converted bool
}

type transcriptionResponse struct {
	Text    string `json:"text"`
	Message string `json:"message"`
	Error   *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

func transcribeAudioMessage(ctx context.Context, filePath string, speech SpeechConfig) (audioTranscript, error) {
	if !speech.Enabled {
		return audioTranscript{}, fmt.Errorf("当前账号未启用语音转写")
	}
	if strings.TrimSpace(speech.APIKey) == "" {
		return audioTranscript{}, fmt.Errorf("缺少语音转写 API key，请配置 speech.api_key 或 codex.api_key")
	}

	prepared, err := ensureAudioReadyForTranscription(ctx, filePath, speech)
	if err != nil {
		return audioTranscript{}, err
	}
	if prepared.Converted {
		defer os.Remove(prepared.FilePath)
	}

	modelCandidates := uniqueStrings(speech.Model, "whisper-1")
	var lastErr error
	for _, model := range modelCandidates {
		text, err := requestOpenAITranscription(ctx, speech, model, prepared.FilePath, prepared.MIMEType)
		if err == nil {
			return audioTranscript{
				Text:             text,
				Model:            model,
				Converted:        prepared.Converted,
				PreparedFilePath: prepared.FilePath,
			}, nil
		}
		lastErr = err
		if model == "whisper-1" || !shouldRetryTranscriptionWithFallbackModel(err) {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("audio transcription failed")
	}
	return audioTranscript{}, lastErr
}

func ensureAudioReadyForTranscription(ctx context.Context, filePath string, speech SpeechConfig) (preparedAudio, error) {
	rawPath := filepath.Clean(strings.TrimSpace(filePath))
	info, err := os.Stat(rawPath)
	if err != nil || !info.Mode().IsRegular() {
		return preparedAudio{}, fmt.Errorf("audio file not found: %s", rawPath)
	}
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(rawPath)))
	if mimeType := transcriptionMimeByExtension[ext]; mimeType != "" {
		return preparedAudio{FilePath: rawPath, MIMEType: mimeType, Converted: false}, nil
	}
	if strings.TrimSpace(speech.FFmpegBin) == "" {
		return preparedAudio{}, fmt.Errorf("语音消息当前格式 %s 需要先转成 wav，但没有可用的 ffmpeg", emptyFallback(ext, "(unknown)"))
	}
	outputPath := filepath.Join(filepath.Dir(rawPath), strings.TrimSuffix(filepath.Base(rawPath), filepath.Ext(rawPath))+"-transcribe.wav")
	if err := convertAudioToWav(ctx, speech.FFmpegBin, rawPath, outputPath); err != nil {
		return preparedAudio{}, err
	}
	return preparedAudio{FilePath: outputPath, MIMEType: "audio/wav", Converted: true}, nil
}

func convertAudioToWav(ctx context.Context, ffmpegBin, inputPath, outputPath string) error {
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-i", inputPath,
		"-vn",
		"-ac", "1",
		"-ar", "16000",
		outputPath,
	}
	cmd := exec.CommandContext(ctx, ffmpegBin, args...)
	stderr, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(stderr))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("ffmpeg exited with error: %s", detail)
	}
	if _, statErr := os.Stat(outputPath); statErr != nil {
		return fmt.Errorf("ffmpeg did not produce output: %s", outputPath)
	}
	return nil
}

func requestOpenAITranscription(ctx context.Context, speech SpeechConfig, model, filePath, mimeType string) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("model", model); err != nil {
		return "", err
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return "", err
	}
	if strings.TrimSpace(speech.Language) != "" {
		if err := writer.WriteField("language", strings.TrimSpace(speech.Language)); err != nil {
			return "", err
		}
	}

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", err
	}
	handle, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer handle.Close()
	if _, err := io.Copy(part, handle); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	endpoint := strings.TrimRight(strings.TrimSpace(speech.BaseURL), "/") + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(speech.APIKey))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if strings.TrimSpace(mimeType) != "" {
		req.Header.Set("X-Audio-Content-Type", mimeType)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var payload transcriptionResponse
	_ = json.Unmarshal(rawBody, &payload)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(firstNonEmpty(payload.Message, payload.ErrorMessage(), string(rawBody)))
		if detail == "" {
			detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return "", &transcriptionError{
			Status:  resp.StatusCode,
			Code:    payload.ErrorCode(),
			Message: fmt.Sprintf("audio transcription failed (%d): %s", resp.StatusCode, detail),
		}
	}
	text := strings.TrimSpace(firstNonEmpty(payload.Text, string(rawBody)))
	if text == "" {
		return "", fmt.Errorf("audio transcription returned empty text")
	}
	return text, nil
}

type transcriptionError struct {
	Status  int
	Code    string
	Message string
}

func (e *transcriptionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func shouldRetryTranscriptionWithFallbackModel(err error) bool {
	var transcriptionErr *transcriptionError
	if errors.As(err, &transcriptionErr) && transcriptionErr != nil && transcriptionErr.Status == http.StatusNotFound {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "model") && (strings.Contains(message, "not found") || strings.Contains(message, "does not exist") || strings.Contains(message, "unsupported"))
}

func (p transcriptionResponse) ErrorMessage() string {
	if p.Error == nil {
		return ""
	}
	return strings.TrimSpace(p.Error.Message)
}

func (p transcriptionResponse) ErrorCode() string {
	if p.Error == nil {
		return ""
	}
	return strings.TrimSpace(p.Error.Code)
}
