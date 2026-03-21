package feishunative

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestSendRequestedAttachmentsFallsBackImageToFile(t *testing.T) {
	tmp := t.TempDir()
	imagePath := filepath.Join(tmp, "chart.png")
	if err := os.WriteFile(imagePath, []byte("fake-image"), 0o644); err != nil {
		t.Fatal(err)
	}

	origUploadImage := uploadLocalImageToFeishuFunc
	origUploadFile := uploadLocalFileToFeishuFunc
	origSendImage := sendImageReplyFunc
	origSendFile := sendFileReplyFunc
	t.Cleanup(func() {
		uploadLocalImageToFeishuFunc = origUploadImage
		uploadLocalFileToFeishuFunc = origUploadFile
		sendImageReplyFunc = origSendImage
		sendFileReplyFunc = origSendFile
	})

	imageUploadCalls := 0
	fileUploadCalls := 0
	fileSendCalls := 0
	uploadLocalImageToFeishuFunc = func(context.Context, *lark.Client, string) (string, string, error) {
		imageUploadCalls++
		return "", "", errors.New("image upload failed")
	}
	uploadLocalFileToFeishuFunc = func(context.Context, *lark.Client, string) (string, string, error) {
		fileUploadCalls++
		return "file_key_1", "chart.png", nil
	}
	sendImageReplyFunc = func(context.Context, *lark.Client, string, string) error {
		t.Fatalf("sendImageReply should not be called on upload failure")
		return nil
	}
	sendFileReplyFunc = func(context.Context, *lark.Client, string, string) error {
		fileSendCalls++
		return nil
	}

	result := sendRequestedAttachments(context.Background(), nil, "oc_chat", []attachmentDirective{
		{Type: "image", Path: imagePath},
	}, tmp)

	if imageUploadCalls != 1 || fileUploadCalls != 1 || fileSendCalls != 1 {
		t.Fatalf("calls imageUpload=%d fileUpload=%d fileSend=%d", imageUploadCalls, fileUploadCalls, fileSendCalls)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("failed = %+v, want empty", result.Failed)
	}
	if len(result.Sent) != 1 || result.Sent[0].Type != "file" {
		t.Fatalf("sent = %+v, want one file fallback", result.Sent)
	}
}

func TestSendRequestedAttachmentsStopsOnCancelledContext(t *testing.T) {
	tmp := t.TempDir()
	filePath1 := filepath.Join(tmp, "a.txt")
	filePath2 := filepath.Join(tmp, "b.txt")
	if err := os.WriteFile(filePath1, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath2, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	origUploadFile := uploadLocalFileToFeishuFunc
	origSendFile := sendFileReplyFunc
	t.Cleanup(func() {
		uploadLocalFileToFeishuFunc = origUploadFile
		sendFileReplyFunc = origSendFile
	})

	ctx, cancel := context.WithCancel(context.Background())
	uploadCalls := 0
	uploadLocalFileToFeishuFunc = func(context.Context, *lark.Client, string) (string, string, error) {
		uploadCalls++
		return "file_key", "a.txt", nil
	}
	sendFileReplyFunc = func(context.Context, *lark.Client, string, string) error {
		cancel()
		return nil
	}

	result := sendRequestedAttachments(ctx, nil, "oc_chat", []attachmentDirective{
		{Type: "file", Path: filePath1},
		{Type: "file", Path: filePath2},
	}, tmp)

	if uploadCalls != 1 {
		t.Fatalf("uploadCalls = %d, want 1", uploadCalls)
	}
	if len(result.Sent) != 1 {
		t.Fatalf("sent = %+v, want one sent attachment", result.Sent)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("failed = %+v, want empty", result.Failed)
	}
}

func TestBuildAttachmentSendResultText(t *testing.T) {
	text := buildAttachmentSendResultText(
		[]sentAttachment{
			{Type: "image", FileName: "chart.png"},
			{Type: "file", FileName: "report.pdf"},
		},
		[]failedAttachment{
			{Type: "file", FileName: "raw.csv", Error: "file too large"},
		},
	)
	want := "[已发送图片] chart.png\n[已发送文件] report.pdf\n[附件发送失败] raw.csv：file too large"
	if text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
}
