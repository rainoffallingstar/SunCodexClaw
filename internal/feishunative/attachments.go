package feishunative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	feishuSendFileDirectivePrefix  = "[[FEISHU_SEND_FILE:"
	feishuSendImageDirectivePrefix = "[[FEISHU_SEND_IMAGE:"
	feishuFileUploadLimit          = 30 * 1024 * 1024
	feishuImageUploadLimit         = 10 * 1024 * 1024
)

var transcriptionMimeByExtension = map[string]string{
	".flac": "audio/flac",
	".m4a":  "audio/mp4",
	".mp3":  "audio/mpeg",
	".mp4":  "audio/mp4",
	".mpeg": "audio/mpeg",
	".mpga": "audio/mpeg",
	".oga":  "audio/ogg",
	".ogg":  "audio/ogg",
	".opus": "audio/ogg",
	".wav":  "audio/wav",
	".webm": "audio/webm",
}

var contentTypeExtensionMap = map[string]string{
	"audio/flac": ".flac",
	"audio/mp3":  ".mp3",
	"audio/mp4":  ".m4a",
	"audio/mpeg": ".mp3",
	"audio/ogg":  ".ogg",
	"audio/opus": ".opus",
	"audio/wav":  ".wav",
	"audio/webm": ".webm",
}

var (
	uploadLocalFileToFeishuFunc  = uploadLocalFileToFeishu
	uploadLocalImageToFeishuFunc = uploadLocalImageToFeishu
	sendFileReplyFunc            = sendFileReply
	sendImageReplyFunc           = sendImageReply
)

type attachmentDirective struct {
	Type string
	Path string
}

type attachmentPlan struct {
	Text        string
	Attachments []attachmentDirective
}

type sentAttachment struct {
	Type     string
	FileName string
}

type failedAttachment struct {
	Type     string
	FileName string
	Error    string
}

type attachmentSendResult struct {
	Sent   []sentAttachment
	Failed []failedAttachment
}

type incomingFile struct {
	FileKey  string
	FileName string
	FileSize int64
}

type incomingImage struct {
	ImageKeys []string
}

type incomingAudio struct {
	FileKey    string
	DurationMS int64
}

type incomingPost struct {
	Text      string
	ImageKeys []string
}

func extractAttachmentDirectives(rawText string) attachmentPlan {
	lines := strings.Split(strings.ReplaceAll(rawText, "\r", ""), "\n")
	kept := make([]string, 0, len(lines))
	attachments := []attachmentDirective{}
	seen := map[string]bool{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, "]]") {
			if strings.HasPrefix(trimmed, feishuSendFileDirectivePrefix) {
				payload := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, feishuSendFileDirectivePrefix), "]]"))
				key := "file:" + payload
				if payload != "" && !seen[key] {
					seen[key] = true
					attachments = append(attachments, attachmentDirective{Type: "file", Path: payload})
				}
				continue
			}
			if strings.HasPrefix(trimmed, feishuSendImageDirectivePrefix) {
				payload := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, feishuSendImageDirectivePrefix), "]]"))
				key := "image:" + payload
				if payload != "" && !seen[key] {
					seen[key] = true
					attachments = append(attachments, attachmentDirective{Type: "image", Path: payload})
				}
				continue
			}
		}
		kept = append(kept, line)
	}
	text := strings.TrimSpace(strings.Join(kept, "\n"))
	text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	return attachmentPlan{Text: text, Attachments: attachments}
}

func sendRequestedAttachments(ctx context.Context, client *lark.Client, chatID string, attachments []attachmentDirective, cwd string) attachmentSendResult {
	result := attachmentSendResult{}
	for _, attachment := range attachments {
		if err := ctx.Err(); err != nil {
			return result
		}
		requestedType := "file"
		if attachment.Type == "image" {
			requestedType = "image"
		}
		resolvedPath := resolveLocalFilePath(attachment.Path, cwd)
		fileName := sanitizeLocalFileName(filepath.Base(emptyFallback(resolvedPath, attachment.Path)), "attachment.bin")
		if resolvedPath == "" {
			result.Failed = append(result.Failed, failedAttachment{Type: requestedType, FileName: fileName, Error: "路径为空"})
			continue
		}
		if requestedType == "image" {
			imageKey, imageName, err := uploadLocalImageToFeishuFunc(ctx, client, resolvedPath)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return result
				}
				fileKey, uploadedName, fileErr := uploadLocalFileToFeishuFunc(ctx, client, resolvedPath)
				if fileErr != nil {
					if errors.Is(fileErr, context.Canceled) || errors.Is(fileErr, context.DeadlineExceeded) {
						return result
					}
					result.Failed = append(result.Failed, failedAttachment{
						Type:     requestedType,
						FileName: fileName,
						Error:    err.Error() + "; 回退文件发送也失败：" + fileErr.Error(),
					})
					continue
				}
				if sendErr := sendFileReplyFunc(ctx, client, chatID, fileKey); sendErr != nil {
					if errors.Is(sendErr, context.Canceled) || errors.Is(sendErr, context.DeadlineExceeded) {
						return result
					}
					result.Failed = append(result.Failed, failedAttachment{
						Type:     requestedType,
						FileName: uploadedName,
						Error:    err.Error() + "; 回退文件发送也失败：" + sendErr.Error(),
					})
					continue
				}
				result.Sent = append(result.Sent, sentAttachment{Type: "file", FileName: uploadedName})
				continue
			}
			if err := sendImageReplyFunc(ctx, client, chatID, imageKey); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return result
				}
				fileKey, uploadedName, fileErr := uploadLocalFileToFeishuFunc(ctx, client, resolvedPath)
				if fileErr != nil {
					if errors.Is(fileErr, context.Canceled) || errors.Is(fileErr, context.DeadlineExceeded) {
						return result
					}
					result.Failed = append(result.Failed, failedAttachment{
						Type:     requestedType,
						FileName: imageName,
						Error:    err.Error() + "; 回退文件发送也失败：" + fileErr.Error(),
					})
					continue
				}
				if err2 := sendFileReplyFunc(ctx, client, chatID, fileKey); err2 != nil {
					if errors.Is(err2, context.Canceled) || errors.Is(err2, context.DeadlineExceeded) {
						return result
					}
					result.Failed = append(result.Failed, failedAttachment{
						Type:     requestedType,
						FileName: uploadedName,
						Error:    err.Error() + "; 回退文件发送也失败：" + err2.Error(),
					})
					continue
				}
				result.Sent = append(result.Sent, sentAttachment{Type: "file", FileName: uploadedName})
				continue
			}
			result.Sent = append(result.Sent, sentAttachment{Type: requestedType, FileName: imageName})
			continue
		}
		fileKey, uploadedName, err := uploadLocalFileToFeishuFunc(ctx, client, resolvedPath)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return result
			}
			result.Failed = append(result.Failed, failedAttachment{Type: requestedType, FileName: fileName, Error: err.Error()})
			continue
		}
		if err := sendFileReplyFunc(ctx, client, chatID, fileKey); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return result
			}
			result.Failed = append(result.Failed, failedAttachment{Type: requestedType, FileName: uploadedName, Error: err.Error()})
			continue
		}
		result.Sent = append(result.Sent, sentAttachment{Type: requestedType, FileName: uploadedName})
	}
	return result
}

func sendFileReply(ctx context.Context, client *lark.Client, chatID, fileKey string) error {
	body, _ := json.Marshal(map[string]string{"file_key": fileKey})
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().ReceiveId(chatID).MsgType(larkim.MsgTypeFile).Content(string(body)).Build()).
		Build()
	resp, err := client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return err
	}
	if resp == nil || !resp.Success() {
		return fmt.Errorf("send file reply failed")
	}
	return nil
}

func sendImageReply(ctx context.Context, client *lark.Client, chatID, imageKey string) error {
	body, _ := json.Marshal(map[string]string{"image_key": imageKey})
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(larkim.NewCreateMessageReqBodyBuilder().ReceiveId(chatID).MsgType(larkim.MsgTypeImage).Content(string(body)).Build()).
		Build()
	resp, err := client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return err
	}
	if resp == nil || !resp.Success() {
		return fmt.Errorf("send image reply failed")
	}
	return nil
}

func uploadLocalFileToFeishu(ctx context.Context, client *lark.Client, localPath string) (string, string, error) {
	resolvedPath := filepath.Clean(localPath)
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", "", fmt.Errorf("file not found: %s", resolvedPath)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("not a file: %s", resolvedPath)
	}
	if info.Size() <= 0 {
		return "", "", fmt.Errorf("file is empty: %s", resolvedPath)
	}
	if info.Size() > feishuFileUploadLimit {
		return "", "", fmt.Errorf("file too large: %s (%s > %s)", filepath.Base(resolvedPath), formatBytes(info.Size()), formatBytes(feishuFileUploadLimit))
	}
	body, err := larkim.NewCreateFilePathReqBodyBuilder().
		FileType(resolveFeishuUploadFileType(resolvedPath)).
		FileName(sanitizeLocalFileName(filepath.Base(resolvedPath), "attachment.bin")).
		FilePath(resolvedPath).
		Build()
	if err != nil {
		return "", "", err
	}
	resp, err := client.Im.V1.File.Create(ctx, larkim.NewCreateFileReqBuilder().Body(body).Build())
	if err != nil {
		return "", "", err
	}
	if resp == nil || !resp.Success() || resp.Data == nil || strings.TrimSpace(deref(resp.Data.FileKey)) == "" {
		return "", "", fmt.Errorf("upload returned empty file_key: %s", resolvedPath)
	}
	return strings.TrimSpace(deref(resp.Data.FileKey)), sanitizeLocalFileName(filepath.Base(resolvedPath), "attachment.bin"), nil
}

func uploadLocalImageToFeishu(ctx context.Context, client *lark.Client, localPath string) (string, string, error) {
	resolvedPath := filepath.Clean(localPath)
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", "", fmt.Errorf("file not found: %s", resolvedPath)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("not a file: %s", resolvedPath)
	}
	if info.Size() <= 0 {
		return "", "", fmt.Errorf("file is empty: %s", filepath.Base(resolvedPath))
	}
	if info.Size() > feishuImageUploadLimit {
		return "", "", fmt.Errorf("image too large: %s (%s > %s)", filepath.Base(resolvedPath), formatBytes(info.Size()), formatBytes(feishuImageUploadLimit))
	}
	if !isImageFilePath(resolvedPath) {
		return "", "", fmt.Errorf("unsupported image extension: %s", emptyFallback(filepath.Ext(resolvedPath), "(none)"))
	}
	body, err := larkim.NewCreateImagePathReqBodyBuilder().
		ImageType("message").
		ImagePath(resolvedPath).
		Build()
	if err != nil {
		return "", "", err
	}
	resp, err := client.Im.V1.Image.Create(ctx, larkim.NewCreateImageReqBuilder().Body(body).Build())
	if err != nil {
		return "", "", err
	}
	if resp == nil || !resp.Success() || resp.Data == nil || strings.TrimSpace(deref(resp.Data.ImageKey)) == "" {
		return "", "", fmt.Errorf("upload returned empty image_key: %s", resolvedPath)
	}
	return strings.TrimSpace(deref(resp.Data.ImageKey)), sanitizeLocalFileName(filepath.Base(resolvedPath), "attachment.bin"), nil
}

func downloadImageToTempFile(ctx context.Context, client *lark.Client, messageID, imageKey string) (string, string, error) {
	tempDir, err := os.MkdirTemp("", "feishu-image-")
	if err != nil {
		return "", "", err
	}
	filePath := filepath.Join(tempDir, fmt.Sprintf("%d-%d.jpg", time.Now().UnixMilli(), os.Getpid()))
	req := larkim.NewGetMessageResourceReqBuilder().
		MessageId(messageID).
		FileKey(imageKey).
		Type("image").
		Build()
	resp, err := client.Im.V1.MessageResource.Get(ctx, req)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", err
	}
	if err := resp.WriteFile(filePath); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", err
	}
	return tempDir, filePath, nil
}

func downloadFileToTempFile(ctx context.Context, client *lark.Client, messageID, fileKey, fileName string) (string, string, string, error) {
	tempDir, err := os.MkdirTemp("", "feishu-file-")
	if err != nil {
		return "", "", "", err
	}
	safeName := sanitizeLocalFileName(fileName, fmt.Sprintf("attachment-%d.bin", time.Now().UnixMilli()))
	filePath := filepath.Join(tempDir, safeName)
	req := larkim.NewGetMessageResourceReqBuilder().
		MessageId(messageID).
		FileKey(fileKey).
		Type("file").
		Build()
	resp, err := client.Im.V1.MessageResource.Get(ctx, req)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", "", err
	}
	if strings.TrimSpace(resp.FileName) != "" {
		safeName = sanitizeLocalFileName(resp.FileName, safeName)
		filePath = filepath.Join(tempDir, safeName)
	}
	if err := resp.WriteFile(filePath); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", "", err
	}
	return tempDir, filePath, safeName, nil
}

func downloadAudioToTempFile(ctx context.Context, client *lark.Client, messageID, fileKey string) (string, string, string, error) {
	tempDir, err := os.MkdirTemp("", "feishu-audio-")
	if err != nil {
		return "", "", "", err
	}
	req := larkim.NewGetMessageResourceReqBuilder().
		MessageId(messageID).
		FileKey(fileKey).
		Type("audio").
		Build()
	resp, err := client.Im.V1.MessageResource.Get(ctx, req)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", "", err
	}
	ext := inferExtensionFromHeaders(resp.Header, ".opus")
	filePath := filepath.Join(tempDir, fmt.Sprintf("voice-%d-%d%s", time.Now().UnixMilli(), os.Getpid(), ext))
	if err := resp.WriteFile(filePath); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", "", "", err
	}
	return tempDir, filePath, ext, nil
}

func parseFileMessageContent(rawContent string) incomingFile {
	var payload struct {
		FileKey  string `json:"file_key"`
		FileName string `json:"file_name"`
		FileSize int64  `json:"file_size"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawContent)), &payload); err != nil {
		return incomingFile{}
	}
	return incomingFile{
		FileKey:  strings.TrimSpace(payload.FileKey),
		FileName: strings.TrimSpace(payload.FileName),
		FileSize: payload.FileSize,
	}
}

func parseImageMessageContent(rawContent string) incomingImage {
	var payload struct {
		ImageKey string `json:"image_key"`
		FileKey  string `json:"file_key"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawContent)), &payload); err != nil {
		return incomingImage{}
	}
	key := strings.TrimSpace(payload.ImageKey)
	if key == "" {
		key = strings.TrimSpace(payload.FileKey)
	}
	if key == "" {
		return incomingImage{}
	}
	return incomingImage{ImageKeys: []string{key}}
}

func parseAudioMessageContent(rawContent string) incomingAudio {
	var payload struct {
		FileKey     string `json:"file_key"`
		FileKeyAlt  string `json:"fileKey"`
		AudioKey    string `json:"audio_key"`
		AudioKeyAlt string `json:"audioKey"`
		Duration    int64  `json:"duration"`
		DurationMS  int64  `json:"duration_ms"`
		DurationAlt int64  `json:"durationMs"`
		Audio       struct {
			FileKey     string `json:"file_key"`
			FileKeyAlt  string `json:"fileKey"`
			AudioKey    string `json:"audio_key"`
			AudioKeyAlt string `json:"audioKey"`
			Duration    int64  `json:"duration"`
			DurationMS  int64  `json:"duration_ms"`
			DurationAlt int64  `json:"durationMs"`
		} `json:"audio"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawContent)), &payload); err != nil {
		return incomingAudio{}
	}
	return incomingAudio{
		FileKey: strings.TrimSpace(firstNonEmpty(
			payload.FileKey,
			payload.FileKeyAlt,
			payload.AudioKey,
			payload.AudioKeyAlt,
			payload.Audio.FileKey,
			payload.Audio.FileKeyAlt,
			payload.Audio.AudioKey,
			payload.Audio.AudioKeyAlt,
		)),
		DurationMS: firstNonZeroInt64(
			payload.DurationMS,
			payload.DurationAlt,
			payload.Duration,
			payload.Audio.DurationMS,
			payload.Audio.DurationAlt,
			payload.Audio.Duration,
		),
	}
}

func parsePostMessageContent(rawContent string) incomingPost {
	content := strings.TrimSpace(rawContent)
	if content == "" {
		return incomingPost{}
	}

	type postLocale struct {
		Title   string             `json:"title"`
		Content [][]map[string]any `json:"content"`
	}
	var payload struct {
		Post map[string]postLocale `json:"post"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return incomingPost{}
	}

	locales := payload.Post
	if len(locales) == 0 {
		return incomingPost{}
	}

	locale := postLocale{}
	switch {
	case locales["zh_cn"].Title != "" || len(locales["zh_cn"].Content) > 0:
		locale = locales["zh_cn"]
	case locales["en_us"].Title != "" || len(locales["en_us"].Content) > 0:
		locale = locales["en_us"]
	default:
		for _, item := range locales {
			locale = item
			break
		}
	}

	textParts := []string{}
	if strings.TrimSpace(locale.Title) != "" {
		textParts = append(textParts, strings.TrimSpace(locale.Title))
	}

	imageKeys := []string{}
	seenImageKeys := map[string]bool{}
	for _, row := range locale.Content {
		for _, item := range row {
			tag := strings.ToLower(strings.TrimSpace(fmt.Sprint(item["tag"])))
			switch tag {
			case "text":
				itemText := strings.TrimSpace(fmt.Sprint(item["text"]))
				if itemText != "" {
					textParts = append(textParts, itemText)
				}
			case "img", "image":
				imageKey := strings.TrimSpace(firstNonEmpty(
					valueAsString(item["image_key"]),
					valueAsString(item["imageKey"]),
					valueAsString(item["file_key"]),
					valueAsString(item["fileKey"]),
				))
				if imageKey != "" && !seenImageKeys[imageKey] {
					seenImageKeys[imageKey] = true
					imageKeys = append(imageKeys, imageKey)
				}
			}
		}
	}

	return incomingPost{
		Text:      strings.TrimSpace(strings.Join(textParts, "\n")),
		ImageKeys: imageKeys,
	}
}

func resolveLocalFilePath(rawFilePath, cwd string) string {
	raw := strings.TrimSpace(rawFilePath)
	raw = strings.Trim(raw, `'"`)
	if raw == "" {
		return ""
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	base := cwd
	if strings.TrimSpace(base) == "" {
		base, _ = os.Getwd()
	}
	return filepath.Clean(filepath.Join(base, raw))
}

func valueAsString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func inferExtensionFromHeaders(headers map[string][]string, fallback string) string {
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(firstHeaderValue(headers, "Content-Type"), ";")[0]))
	if ext := strings.TrimSpace(contentTypeExtensionMap[contentType]); ext != "" {
		return ext
	}
	name := parseDispositionFileName(firstHeaderValue(headers, "Content-Disposition"))
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(name)))
	if ext != "" {
		return ext
	}
	return fallback
}

func firstHeaderValue(headers map[string][]string, key string) string {
	for headerKey, values := range headers {
		if strings.EqualFold(headerKey, key) && len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

func parseDispositionFileName(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	for _, prefix := range []string{"filename*=", "filename="} {
		idx := strings.Index(strings.ToLower(raw), prefix)
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(raw[idx+len(prefix):])
		if strings.HasPrefix(strings.ToLower(prefix), "filename*=") {
			if parts := strings.SplitN(name, "''", 2); len(parts) == 2 {
				name = parts[1]
			}
		}
		name = strings.Trim(name, `"`)
		name = strings.TrimSpace(name)
		if name != "" {
			return sanitizeLocalFileName(name, "")
		}
	}
	return ""
}

func formatDurationFromMS(durationMS int64) string {
	ms := durationMS
	if ms < 0 {
		ms = 0
	}
	if ms == 0 {
		return ""
	}
	totalSeconds := int((ms + 500) / 1000)
	if totalSeconds < 60 {
		return fmt.Sprintf("%d 秒", totalSeconds)
	}
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	if seconds == 0 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	return fmt.Sprintf("%d 分 %d 秒", minutes, seconds)
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func isImageFilePath(filePath string) bool {
	switch strings.ToLower(strings.TrimSpace(filepath.Ext(filePath))) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".tiff", ".bmp", ".ico":
		return true
	default:
		return false
	}
}

func sanitizeLocalFileName(rawName, fallback string) string {
	name := filepath.Base(strings.TrimSpace(rawName))
	if name == "" {
		name = fallback
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 32:
			b.WriteRune('_')
		case strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	cleaned := strings.TrimSpace(b.String())
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return fallback
	}
	if len([]rune(cleaned)) > 180 {
		return string([]rune(cleaned)[:180])
	}
	return cleaned
}

func formatBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB"}
	size := float64(bytes)
	idx := 0
	for size >= 1024 && idx < len(units)-1 {
		size /= 1024
		idx++
	}
	switch {
	case size >= 100 || idx == 0:
		return fmt.Sprintf("%.0f %s", size, units[idx])
	case size >= 10:
		return fmt.Sprintf("%.1f %s", size, units[idx])
	default:
		return fmt.Sprintf("%.2f %s", size, units[idx])
	}
}

func resolveFeishuUploadFileType(filePath string) string {
	switch strings.ToLower(strings.TrimSpace(filepath.Ext(filePath))) {
	case ".pdf":
		return "pdf"
	case ".doc", ".docx":
		return "doc"
	case ".xls", ".xlsx", ".csv":
		return "xls"
	case ".ppt", ".pptx", ".key":
		return "ppt"
	case ".mp4", ".mov", ".m4v":
		return "mp4"
	case ".opus":
		return "opus"
	default:
		return "stream"
	}
}

func buildAttachmentFailureReply(sent []sentAttachment, failed []failedAttachment) string {
	if len(failed) == 0 {
		return ""
	}
	details := make([]string, 0, len(failed))
	for _, item := range failed {
		details = append(details, fmt.Sprintf("%s（%s）", item.FileName, item.Error))
	}
	if len(sent) == 0 {
		return compactText("附件发送失败："+strings.Join(details, "；"), 1800)
	}
	return compactText(fmt.Sprintf("已发送 %d 个附件；以下附件发送失败：%s", len(sent), strings.Join(details, "；")), 1800)
}

func buildAttachmentSendResultText(sent []sentAttachment, failed []failedAttachment) string {
	lines := []string{}
	sentImages := []string{}
	sentFiles := []string{}
	for _, item := range sent {
		switch item.Type {
		case "image":
			sentImages = append(sentImages, item.FileName)
		case "file":
			sentFiles = append(sentFiles, item.FileName)
		}
	}
	if len(sentImages) > 0 {
		lines = append(lines, "[已发送图片] "+strings.Join(sentImages, "，"))
	}
	if len(sentFiles) > 0 {
		lines = append(lines, "[已发送文件] "+strings.Join(sentFiles, "，"))
	}
	if len(failed) > 0 {
		failures := make([]string, 0, len(failed))
		for _, item := range failed {
			failures = append(failures, item.FileName+"："+item.Error)
		}
		lines = append(lines, "[附件发送失败] "+strings.Join(failures, "；"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func buildDefaultAttachmentReply(attachments []sentAttachment) string {
	imageCount := 0
	fileCount := 0
	for _, item := range attachments {
		if item.Type == "image" {
			imageCount++
		} else if item.Type == "file" {
			fileCount++
		}
	}
	switch {
	case imageCount > 0 && fileCount == 0:
		return "图片已发送，请查收。"
	case fileCount > 0 && imageCount == 0:
		return "文件已发送，请查收。"
	case imageCount > 0 || fileCount > 0:
		return "附件已发送，请查收。"
	default:
		return ""
	}
}
