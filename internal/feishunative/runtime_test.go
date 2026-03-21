package feishunative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type progressSpy struct {
	calls []string
}

func (p *progressSpy) complete(context.Context, string) {
	p.calls = append(p.calls, "complete")
}

func (p *progressSpy) abort(context.Context, string) {
	p.calls = append(p.calls, "abort")
}

func (p *progressSpy) fail(context.Context, string) {
	p.calls = append(p.calls, "fail")
}

func (p *progressSpy) recordFinalReply(context.Context, string) {
	p.calls = append(p.calls, "record_final_reply")
}

func (p *progressSpy) recordEvent(context.Context, codexProgressEvent) {
	p.calls = append(p.calls, "record_event")
}

func TestRunDryRun(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(repo, "workspace", "assistant")
	if err := os.WriteFile(filepath.Join(repo, "config", "feishu", "bots.toml"), []byte(strings.Join([]string{
		"[bot.assistant]",
		"bot_name = \"助手\"",
		"[bot.assistant.progress]",
		"mode = \"doc\"",
		"[bot.assistant.progress.doc]",
		"title_prefix = \"测试进度\"",
		"share_to_chat = true",
		"link_scope = \"same_tenant\"",
		"include_user_message = true",
		"write_final_reply = true",
		"[bot.assistant.codex]",
		"cwd = " + quote(workspace),
		"bin = \"go\"",
		"model = \"gpt-5.2\"",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(strings.Join([]string{
		"[feishu.assistant]",
		"app_id = \"cli_xxx\"",
		"app_secret = \"secret_xxx\"",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Run(context.Background(), RunOptions{
		RepoRoot: repo,
		Account:  "assistant",
		DryRun:   true,
		Stdout:   &out,
		Stderr:   &out,
	}); err != nil {
		t.Fatalf("Run(dry-run) error = %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "FEISHU_WS_DRY_RUN") {
		t.Fatalf("dry run output missing header: %s", text)
	}
	if !strings.Contains(text, "account=assistant") {
		t.Fatalf("dry run output missing account: %s", text)
	}
	if !strings.Contains(text, "codex_bin=go") {
		t.Fatalf("dry run output missing codex bin: %s", text)
	}
	if !strings.Contains(text, "progress_doc_title_prefix=测试进度") {
		t.Fatalf("dry run output missing progress doc title: %s", text)
	}
}

func TestEnsureRuntimeWorkspaceCreatesDocs(t *testing.T) {
	repo := t.TempDir()
	workspace := filepath.Join(repo, "workspace", "assistant")
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(strings.Join([]string{
		"[sync.assistant]",
		"workspace_id = \"assistant-private\"",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		RepoRoot:       repo,
		AccountName:    "assistant",
		ConfigPath:     filepath.Join(repo, "config", "feishu", "bots.toml"),
		BotName:        "助手",
		MentionAliases: []string{"助手"},
		Progress:       ProgressConfig{Mode: "doc"},
		Codex:          CodexConfig{Cwd: workspace},
	}

	result, err := EnsureRuntimeWorkspace(repo, cfg)
	if err != nil {
		t.Fatalf("EnsureRuntimeWorkspace() error = %v", err)
	}
	if !result.ConfigWritten {
		t.Fatalf("expected config to be written")
	}
	if len(result.CreatedDocs) != 3 {
		t.Fatalf("created docs = %d, want 3", len(result.CreatedDocs))
	}
	for _, name := range []string{".config.toml", "agent.md", "soul.md", "heartbeats.md"} {
		if _, err := os.Stat(filepath.Join(workspace, name)); err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
	}
	configBody, err := os.ReadFile(filepath.Join(workspace, ".config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(.config.toml) error = %v", err)
	}
	if !strings.Contains(string(configBody), `sync_workspace_id = "assistant-private"`) {
		t.Fatalf(".config.toml missing resolved sync workspace id:\n%s", string(configBody))
	}
	timerPath := filepath.Join(repo, "config", "timers", "assistant", "workspace-doc-sync.json")
	if _, err := os.Stat(timerPath); err != nil {
		t.Fatalf("default sync timer not created: %v", err)
	}
	timerBody, err := os.ReadFile(timerPath)
	if err != nil {
		t.Fatalf("ReadFile(timer) error = %v", err)
	}
	var task struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(timerBody, &task); err != nil {
		t.Fatalf("json.Unmarshal(timer) error = %v", err)
	}
	if task.WorkspaceID != "assistant-private" {
		t.Fatalf("workspace-doc-sync workspace_id = %q, want assistant-private", task.WorkspaceID)
	}
}

func TestEnsureRuntimeWorkspaceRequiresAccount(t *testing.T) {
	repo := t.TempDir()
	workspace := filepath.Join(repo, "workspace", "unknown")
	_, err := EnsureRuntimeWorkspace(repo, Config{
		RepoRoot: repo,
		Codex:    CodexConfig{Cwd: workspace},
	})
	if err == nil || !strings.Contains(err.Error(), "missing workspace account") {
		t.Fatalf("EnsureRuntimeWorkspace() error = %v, want missing workspace account", err)
	}
}

func TestEnsureRuntimeWorkspaceUsesSanitizedMemoryAndTimerPaths(t *testing.T) {
	repo := t.TempDir()
	workspace := filepath.Join(repo, "workspace", "assistant bot")
	cfg := Config{
		RepoRoot:       repo,
		AccountName:    "assistant bot",
		ConfigPath:     filepath.Join(repo, "config", "feishu", "bots.toml"),
		BotName:        "助手",
		MentionAliases: []string{"助手"},
		Progress:       ProgressConfig{Mode: "doc"},
		Codex:          CodexConfig{Cwd: workspace},
	}

	result, err := EnsureRuntimeWorkspace(repo, cfg)
	if err != nil {
		t.Fatalf("EnsureRuntimeWorkspace() error = %v", err)
	}
	configBody, err := os.ReadFile(filepath.Join(workspace, ".config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(.config.toml) error = %v", err)
	}
	text := string(configBody)
	if !strings.Contains(text, filepath.ToSlash(filepath.Join(repo, "config", "memory", "libraries", "assistant-bot"))) {
		t.Fatalf(".config.toml missing sanitized memory path:\n%s", text)
	}
	if !strings.Contains(text, filepath.ToSlash(filepath.Join(repo, "config", "timers", "assistant-bot"))) {
		t.Fatalf(".config.toml missing sanitized timer path:\n%s", text)
	}
	if !strings.Contains(text, `config_table = "[bot.\"assistant bot\"]"`) {
		t.Fatalf(".config.toml missing quoted config_table for special account:\n%s", text)
	}
	if !strings.Contains(result.DefaultSyncTaskPath, filepath.Join("config", "timers", "assistant-bot", "workspace-doc-sync.json")) {
		t.Fatalf("DefaultSyncTaskPath = %q, want sanitized timer namespace path", result.DefaultSyncTaskPath)
	}
	if _, err := os.Stat(filepath.Join(repo, "config", "timers", "assistant-bot", "workspace-doc-sync.json")); err != nil {
		t.Fatalf("sanitized default sync timer not created: %v", err)
	}
}

func TestLoadPrefersAccountSecretsOverBotOverlay(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "feishu", "bots.toml"), []byte(strings.Join([]string{
		"[shared]",
		"reply_prefix = \"shared\"",
		"[bot.assistant]",
		"reply_prefix = \"overlay-private\"",
		"bot_name = \"助手\"",
		"[bot.assistant.codex]",
		"cwd = \"workspace/assistant\"",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(strings.Join([]string{
		"[feishu.default]",
		"reply_prefix = \"secret-default\"",
		"[feishu.assistant]",
		"app_id = \"cli_xxx\"",
		"app_secret = \"secret_xxx\"",
		"reply_prefix = \"secret-private\"",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(repo, "assistant")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ReplyPrefix != "secret-private" {
		t.Fatalf("ReplyPrefix = %q, want secret-private", cfg.ReplyPrefix)
	}
	if cfg.BotName != "助手" {
		t.Fatalf("BotName = %q, want 助手", cfg.BotName)
	}
}

func TestResolveSyncConfigUsesSameWorkspaceFallbacksAsCLI(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "config", "secrets", "local.toml"), []byte(strings.Join([]string{
		"[feishu.assistant.codex]",
		"cwd = \"workspace/private\"",
		"[sync.assistant]",
		"workspace_id = \"assistant-private\"",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, workspaceDir, err := ResolveSyncConfig(repo, "assistant", SyncConfigOptions{})
	if err != nil {
		t.Fatalf("ResolveSyncConfig() error = %v", err)
	}
	if workspaceDir != filepath.Join(repo, "workspace", "private") {
		t.Fatalf("workspaceDir = %q, want %q", workspaceDir, filepath.Join(repo, "workspace", "private"))
	}
	if cfg.WorkspaceID != "assistant-private" {
		t.Fatalf("WorkspaceID = %q, want assistant-private", cfg.WorkspaceID)
	}
}

func TestParseAdminCommands(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		kind   string
		action string
		target string
		query  string
	}{
		{"timer list", "/timer list", "timer", "list", "", ""},
		{"timer alias", "/timers", "timer", "list", "", ""},
		{"timer show", "/timer show daily-report", "timer", "show", "daily-report", ""},
		{"memory add", "/memory 以后默认用中文回复", "memory", "add", "", ""},
		{"memory search", "/memory search 中文", "memory", "search", "", "中文"},
		{"sync pull", "/sync pull latest", "sync", "pull", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cmd *adminCommand
			switch tt.kind {
			case "timer":
				cmd = parseTimerCommand(tt.text)
			case "memory":
				cmd = parseMemoryCommand(tt.text)
			case "sync":
				cmd = parseSyncCommand(tt.text)
			}
			if cmd == nil {
				t.Fatalf("command is nil")
			}
			if cmd.Kind != tt.kind || cmd.Action != tt.action {
				t.Fatalf("got kind=%s action=%s", cmd.Kind, cmd.Action)
			}
			if tt.target != "" && cmd.Target != tt.target {
				t.Fatalf("target=%q want %q", cmd.Target, tt.target)
			}
			if tt.query != "" && cmd.Query != tt.query {
				t.Fatalf("query=%q want %q", cmd.Query, tt.query)
			}
		})
	}
}

func TestExtractAttachmentDirectives(t *testing.T) {
	plan := extractAttachmentDirectives(strings.Join([]string{
		"第一段回复",
		"[[FEISHU_SEND_IMAGE:./out/chart.png]]",
		"",
		"[[FEISHU_SEND_FILE: ./out/report.pdf ]]",
		"第二段回复",
		"[[FEISHU_SEND_IMAGE:./out/chart.png]]",
	}, "\n"))

	if got, want := plan.Text, "第一段回复\n\n第二段回复"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	if len(plan.Attachments) != 2 {
		t.Fatalf("attachments = %d, want 2", len(plan.Attachments))
	}
	if plan.Attachments[0].Type != "image" || plan.Attachments[0].Path != "./out/chart.png" {
		t.Fatalf("first attachment = %+v", plan.Attachments[0])
	}
	if plan.Attachments[1].Type != "file" || plan.Attachments[1].Path != "./out/report.pdf" {
		t.Fatalf("second attachment = %+v", plan.Attachments[1])
	}
}

func TestParsePostMessageContent(t *testing.T) {
	raw := `{
		"post": {
			"zh_cn": {
				"title": "每日报告",
				"content": [
					[
						{"tag": "text", "text": "先看这张图"},
						{"tag": "img", "image_key": "img-1"}
					],
					[
						{"tag": "text", "text": "再看结论"},
						{"tag": "image", "file_key": "img-2"},
						{"tag": "img", "image_key": "img-1"}
					]
				]
			}
		}
	}`

	parsed := parsePostMessageContent(raw)
	if got, want := parsed.Text, "每日报告\n先看这张图\n再看结论"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	if got, want := len(parsed.ImageKeys), 2; got != want {
		t.Fatalf("image key count = %d, want %d", got, want)
	}
	if parsed.ImageKeys[0] != "img-1" || parsed.ImageKeys[1] != "img-2" {
		t.Fatalf("image keys = %#v", parsed.ImageKeys)
	}
}

func TestParseAudioMessageContent(t *testing.T) {
	raw := `{"audio":{"audio_key":"audio-1","durationMs":12500}}`
	parsed := parseAudioMessageContent(raw)
	if parsed.FileKey != "audio-1" {
		t.Fatalf("file key = %q", parsed.FileKey)
	}
	if parsed.DurationMS != 12500 {
		t.Fatalf("duration = %d", parsed.DurationMS)
	}
}

func TestFormatDurationFromMS(t *testing.T) {
	tests := []struct {
		ms   int64
		want string
	}{
		{0, ""},
		{8_000, "8 秒"},
		{60_000, "1 分钟"},
		{75_000, "1 分 15 秒"},
	}
	for _, tt := range tests {
		if got := formatDurationFromMS(tt.ms); got != tt.want {
			t.Fatalf("formatDurationFromMS(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestBuildFakeStreamSteps(t *testing.T) {
	steps := buildFakeStreamSteps("你好世界今天", 2, 3)
	if len(steps) < 2 {
		t.Fatalf("steps = %#v", steps)
	}
	if steps[len(steps)-1] != "你好世界今天" {
		t.Fatalf("last step = %q", steps[len(steps)-1])
	}
	for i := 1; i < len(steps); i++ {
		if len([]rune(steps[i])) < len([]rune(steps[i-1])) {
			t.Fatalf("steps not monotonic: %#v", steps)
		}
	}
}

func TestFormatCodexProgressEvent(t *testing.T) {
	event := codexProgressEvent{
		"type": "item.started",
		"item": map[string]any{
			"type": "command_execution",
		},
	}
	if got := formatCodexProgressEvent(event); got != "开始步骤：command_execution" {
		t.Fatalf("formatCodexProgressEvent() = %q", got)
	}
}

func TestFormatCodexProgressEventForDoc(t *testing.T) {
	event := codexProgressEvent{
		"type": "item.completed",
		"item": map[string]any{
			"type": "command_execution",
		},
		"parsed_cmd": map[string]any{
			"command": "go test ./...",
		},
		"working_directory": "/workspace",
		"exit_code":         0,
	}
	formatted := formatCodexProgressEventForDoc(event)
	if formatted.Kind != "detail" {
		t.Fatalf("kind = %q", formatted.Kind)
	}
	if formatted.Title == "" {
		t.Fatalf("title should not be empty")
	}
	if len(formatted.Meta) == 0 {
		t.Fatalf("meta should not be empty")
	}
	if len(formatted.Sections) == 0 {
		t.Fatalf("sections should not be empty")
	}
	if formatted.Sections[0].Format != "code" {
		t.Fatalf("first section format = %q, want code", formatted.Sections[0].Format)
	}
}

func TestBuildPromptIncludesSystemGuides(t *testing.T) {
	cfg := Config{
		AccountName: "assistant",
		RepoRoot:    "/repo",
		Codex: CodexConfig{
			Cwd:          "/repo/workspace/assistant",
			SystemPrompt: "你是测试助手",
		},
	}
	prompt := buildPrompt(cfg, "oc_chat", "请帮我设置一个定时任务", "测试线程", nil)
	for _, want := range []string{
		"定时任务系统：",
		"记忆系统：",
		"文档同步系统：",
		"如果 SSH、curl、nc 或其他网络命令失败",
		"可以输出多行附件指令",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q\n%s", want, prompt)
		}
	}
}

func TestBuildResumePromptIncludesSystemGuides(t *testing.T) {
	cfg := Config{AccountName: "assistant"}
	prompt := buildResumePrompt(cfg, "oc_chat", "继续处理", 2)
	for _, want := range []string{
		"定时任务系统：",
		"记忆系统：",
		"文档同步系统：",
		"附加图片：2 张",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("resume prompt missing %q\n%s", want, prompt)
		}
	}
}

func TestBuildCodexExecutionFailureReplyAddsResponsesHint(t *testing.T) {
	reply := buildCodexExecutionFailureReply(errors.New("codex exec failed: responses_websocket 400 Bad Request url: ws://demo/v1/responses"))
	if !strings.Contains(reply, "处理失败：Codex 执行失败。") {
		t.Fatalf("reply missing failure summary: %s", reply)
	}
	if !strings.Contains(reply, "Responses WebSocket") {
		t.Fatalf("reply missing websocket hint: %s", reply)
	}
	if !strings.Contains(reply, "详情：") {
		t.Fatalf("reply missing details: %s", reply)
	}
}

func TestBuildCodexExecutionFailureReplyExplainsUnsupportedGateway(t *testing.T) {
	reply := buildCodexExecutionFailureReply(errors.New("codex exec failed: gateway_reachable_but_responses_websocket_unsupported status=400 Bad Request body=Bad Request error=websocket: bad handshake"))
	for _, want := range []string{
		"当前地址已经可达",
		"/v1/responses",
		"Tailscale 路由中断",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply missing %q: %s", want, reply)
		}
	}
}

func TestHandleMessageEventSendsFinalErrorReplyWhenCodexFails(t *testing.T) {
	previousRunCodex := runCodexFunc
	previousSendTextReply := sendTextReplyFunc
	t.Cleanup(func() {
		runCodexFunc = previousRunCodex
		sendTextReplyFunc = previousSendTextReply
	})

	runErr := errors.New("codex exec failed: responses_websocket 400 Bad Request url: ws://demo/v1/responses")
	runCodexFunc = func(context.Context, CodexConfig, CodexRunRequest) (CodexReply, error) {
		return CodexReply{}, runErr
	}

	var sentChatID string
	var sentText string
	sendTextReplyFunc = func(ctx context.Context, client *lark.Client, chatID, text string) error {
		sentChatID = chatID
		sentText = text
		return nil
	}

	cfg := Config{
		AccountName: "assistant",
		AutoReply:   true,
		ReplyMode:   "codex",
	}
	chatID := "oc_test_chat"
	messageID := "om_test_message"
	messageType := "text"
	chatType := "p2p"
	content := `{"text":"你好"}`
	senderType := "user"
	envelope := &messageEnvelope{
		Event: &larkim.P2MessageReceiveV1{
			Event: &larkim.P2MessageReceiveV1Data{
				Sender: &larkim.EventSender{
					SenderType: &senderType,
				},
				Message: &larkim.EventMessage{
					ChatId:      &chatID,
					MessageId:   &messageID,
					MessageType: &messageType,
					ChatType:    &chatType,
					Content:     &content,
				},
			},
		},
		Scope: conversationScope{
			TaskKey:  chatID,
			StateKey: chatID,
			Kind:     "p2p",
		},
	}

	err := handleMessageEvent(context.Background(), nil, cfg, envelope, nil)
	if !errors.Is(err, runErr) {
		t.Fatalf("handleMessageEvent() error = %v, want %v", err, runErr)
	}
	if sentChatID != chatID {
		t.Fatalf("sent chat id = %q, want %q", sentChatID, chatID)
	}
	if !strings.Contains(sentText, "处理失败：Codex 执行失败。") {
		t.Fatalf("sent text missing summary: %s", sentText)
	}
	if !strings.Contains(sentText, "Responses WebSocket") {
		t.Fatalf("sent text missing websocket hint: %s", sentText)
	}
	if !strings.Contains(sentText, "详情：") {
		t.Fatalf("sent text missing details: %s", sentText)
	}
}

func TestShouldRenderFeishuMarkdown(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"普通纯文本回复", false},
		{"# 标题\n\n- 一项\n- 二项", true},
		{"这里有 `inline code`", true},
		{"1. 第一项\n2. 第二项", true},
	}
	for _, tt := range tests {
		if got := shouldRenderFeishuMarkdown(tt.text); got != tt.want {
			t.Fatalf("shouldRenderFeishuMarkdown(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestParseIncomingTextForPost(t *testing.T) {
	raw := `{"post":{"en_us":{"title":"Hello","content":[[{"tag":"text","text":"world"}]]}}}`
	text, mention := parseIncomingText("post", raw)
	if text != "Hello\nworld" {
		t.Fatalf("text = %q", text)
	}
	if mention != "" {
		t.Fatalf("mention = %q, want empty", mention)
	}
}

func TestNormalizeIncomingTextStripsMentionMarkup(t *testing.T) {
	text := normalizeIncomingText(" @_user_1\u00a0<at user_id=\"ou_bot\">助手</at>  请看一下 ", []*larkim.MentionEvent{
		{
			Key:  stringPtr("@_user_1"),
			Name: stringPtr("助手"),
			Id: &larkim.UserId{
				OpenId: stringPtr("ou_bot"),
			},
		},
	}, []string{"助手"})
	if text != "请看一下" {
		t.Fatalf("text = %q, want 请看一下", text)
	}
}

func TestDetectTextualBotMentionSupportsFlexibleWhitespace(t *testing.T) {
	got := detectTextualBotMention("请 @ AI   助手 看一下", []string{"AI 助手"})
	if got != "AI 助手" {
		t.Fatalf("detectTextualBotMention() = %q, want AI 助手", got)
	}
}

func TestShouldReplyUsesRawTextMentionEvenAfterNormalization(t *testing.T) {
	cfg := Config{
		RequireMention: true,
		MentionAliases: []string{"助手"},
	}
	if !shouldReply(cfg, &larkim.EventMessage{}, "group", "请看一下", "助手", false) {
		t.Fatalf("expected textual mention to allow reply")
	}
}

func TestBuildSyncAdminArgsRestoreDoesNotForceOverwrite(t *testing.T) {
	args := buildSyncAdminArgs(&adminCommand{Kind: "sync", Action: "restore", Snapshot: "latest"}, "assistant")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "sync restore --account assistant --snapshot latest") {
		t.Fatalf("unexpected args: %v", args)
	}
	if strings.Contains(joined, "--force") {
		t.Fatalf("restore args should not force overwrite: %v", args)
	}
}

func TestBuildSyncAdminArgsPullUsesAccountScopedRestoreDir(t *testing.T) {
	args := buildSyncAdminArgs(&adminCommand{Kind: "sync", Action: "pull", Snapshot: "latest"}, "assistant.bot")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--to .runtime/sync/restore/assistant.bot/latest") {
		t.Fatalf("pull args should use account-scoped restore dir: %v", args)
	}
}

func TestRequireAdminCommandAccount(t *testing.T) {
	if _, err := requireAdminCommandAccount(Config{}); err == nil {
		t.Fatalf("requireAdminCommandAccount() error = nil, want error")
	}
	got, err := requireAdminCommandAccount(Config{AccountName: "assistant"})
	if err != nil {
		t.Fatalf("requireAdminCommandAccount() error = %v", err)
	}
	if got != "assistant" {
		t.Fatalf("requireAdminCommandAccount() = %q, want assistant", got)
	}
}

func TestBuildSyncAdminArgsRequiresAccount(t *testing.T) {
	args := buildSyncAdminArgs(&adminCommand{Kind: "sync", Action: "status"}, "")
	if args != nil {
		t.Fatalf("buildSyncAdminArgs() = %v, want nil", args)
	}
}

func TestResolveMentionAliasesDoesNotInferBrokenSystemPromptFragments(t *testing.T) {
	got := resolveMentionAliases("飞书 Codex 助手", nil, "AI 助手：", "你是“飞书 Codex 助手”，通过飞书和用户交流。", "AI 助手｜任务进度")
	text := strings.Join(got, " | ")
	if strings.Contains(text, "“飞书") {
		t.Fatalf("resolveMentionAliases() = %v, should not include broken system prompt fragment", got)
	}
	if !strings.Contains(text, "飞书 Codex 助手") {
		t.Fatalf("resolveMentionAliases() = %v, want bot name alias", got)
	}
}

func TestWalkRenderedReplyChunksStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := walkRenderedReplyChunks(ctx, strings.Repeat("第一行\n", 300), false, 120, func(chunk string) error {
		calls++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestDocProgressReporterActivateFallbackUsesSilentReporter(t *testing.T) {
	reporter := &docProgressReporter{
		chatID:      "oc_chat",
		cfg:         Config{},
		stepHistory: []string{"第一步", "第二步"},
	}
	if err := reporter.activateFallback(context.Background(), "test", errors.New("boom")); err != nil {
		t.Fatalf("activateFallback() error = %v", err)
	}
	if reporter.fallback == nil {
		t.Fatalf("expected fallback reporter")
	}
	if _, ok := reporter.fallback.(silentProgressReporter); !ok {
		t.Fatalf("fallback = %T, want silentProgressReporter", reporter.fallback)
	}
}

func TestFinishProgressReporterCompletesBeforeFinalReply(t *testing.T) {
	spy := &progressSpy{}
	finishProgressReporter(context.Background(), spy, "done", "final")
	if got, want := strings.Join(spy.calls, ","), "complete,record_final_reply"; got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestIsSupportedIncomingMessageType(t *testing.T) {
	for _, value := range []string{"text", "image", "post", "file", "audio"} {
		if !isSupportedIncomingMessageType(value) {
			t.Fatalf("expected %q to be supported", value)
		}
	}
	if isSupportedIncomingMessageType("sticker") {
		t.Fatalf("expected sticker to be unsupported")
	}
}

func TestSplitRawTextChunksPreservesNewlines(t *testing.T) {
	text := strings.Repeat("line\n", 80)
	chunks := splitRawTextChunks(text, 120)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %#v", chunks)
	}
	joined := strings.Join(chunks, "")
	if joined != text {
		t.Fatalf("joined = %q, want %q", joined, text)
	}
}

func TestThreadCommandsAndReset(t *testing.T) {
	state := &chatState{
		Threads:         map[string]*threadState{"t1": makeThread("t1", "主线程")},
		Order:           []string{"t1"},
		CurrentThreadID: "t1",
		NextThreadSeq:   2,
	}

	handled, reply := handleThreadCommand(state, parseThreadCommand("/thread new 规划"))
	if !handled || !strings.Contains(reply, "已创建并切换到新线程") {
		t.Fatalf("new thread reply = %q", reply)
	}
	if state.CurrentThreadID != "t2" {
		t.Fatalf("current thread = %s, want t2", state.CurrentThreadID)
	}

	appendHistory(getCurrentThread(state), "user", "hello", 6)
	appendHistory(getCurrentThread(state), "assistant", "world", 6)

	handled, reply = handleThreadCommand(state, parseThreadCommand("/thread switch t1"))
	if !handled || !strings.Contains(reply, "已切换到线程") {
		t.Fatalf("switch reply = %q", reply)
	}
	if state.CurrentThreadID != "t1" {
		t.Fatalf("current thread = %s, want t1", state.CurrentThreadID)
	}

	handled, reply = handleThreadCommand(state, parseThreadCommand("/threads"))
	if !handled || !strings.Contains(reply, "线程列表") {
		t.Fatalf("list reply = %q", reply)
	}

	if !isResetCommand("/reset") {
		t.Fatalf("expected /reset to be recognized")
	}
	msg, ok := resetCurrentThread(state)
	if !ok || !strings.Contains(msg, "已清空当前线程上下文") {
		t.Fatalf("reset reply = %q ok=%v", msg, ok)
	}
}

func TestBuildConversationScope(t *testing.T) {
	p2p := buildConversationScope("oc_1", "p2p", "ou_1", "om_1")
	if p2p.TaskKey != "oc_1" || p2p.StateKey != "oc_1" || p2p.Kind != "p2p" {
		t.Fatalf("unexpected p2p scope: %+v", p2p)
	}

	group := buildConversationScope("oc_group", "group", "ou_sender", "om_2")
	if group.TaskKey != "oc_group::ou_sender" || group.StateKey != "oc_group::ou_sender" || group.Kind != "group_sender" {
		t.Fatalf("unexpected group scope: %+v", group)
	}
}

func TestPrepareMessageEnvelopeAllowsMentionCarry(t *testing.T) {
	cfg := Config{
		RequireMention: true,
		MentionAliases: []string{"助手"},
		BotOpenID:      ResolvedValue{Value: "ou_bot"},
	}
	store := newRecentMentionStore()

	first := prepareMessageEnvelope(cfg, buildTestEvent("oc_group", "group", "om_1", "text", `{"text":"@助手 看一下"}`, "ou_sender"), store)
	if first == nil {
		t.Fatalf("first envelope is nil")
	}
	if !first.Meta.ExplicitBotMention {
		t.Fatalf("expected explicit mention to be true")
	}
	if first.Meta.TextMentionAlias != "助手" {
		t.Fatalf("text mention alias = %q, want 助手", first.Meta.TextMentionAlias)
	}
	if first.Scope.StateKey != "oc_group::ou_sender" {
		t.Fatalf("state key = %q", first.Scope.StateKey)
	}

	second := prepareMessageEnvelope(cfg, buildTestEvent("oc_group", "group", "om_2", "image", `{"image_key":"img-1"}`, "ou_sender"), store)
	if second == nil {
		t.Fatalf("second envelope is nil")
	}
	if !second.Meta.AllowMentionCarry {
		t.Fatalf("expected mention carry to be enabled")
	}
	if second.Meta.CarryAge < 0 {
		t.Fatalf("carry age = %s, want non-negative", second.Meta.CarryAge)
	}
	if second.ShouldSupersedeActiveTask {
		t.Fatalf("image follow-up should not supersede active task")
	}
}

func TestPrepareMessageEnvelopeAutodetectsBotOpenIDBeforeMentionCarry(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		RepoRoot:       repo,
		AccountName:    "assistant",
		RequireMention: true,
		MentionAliases: []string{"助手"},
	}
	globalRuntimeBotOpenIDs = &runtimeBotOpenIDStore{values: map[string]ResolvedValue{}}
	store := newRecentMentionStore()

	first := buildTestEvent("oc_group", "group", "om_mention_1", "text", `{"text":"看一下这个"}`, "ou_sender")
	first.Event.Message.Mentions = []*larkim.MentionEvent{
		{
			Name: stringPtr("助手"),
			Id: &larkim.UserId{
				OpenId: stringPtr("ou_bot"),
			},
		},
	}
	envelope := prepareMessageEnvelope(cfg, first, store)
	if envelope == nil {
		t.Fatalf("first envelope is nil")
	}
	if !envelope.Meta.ExplicitBotMention {
		t.Fatalf("expected explicit mention after bot_open_id autodetect")
	}
	if envelope.Meta.MentionNameAlias != "助手" {
		t.Fatalf("mention name alias = %q, want 助手", envelope.Meta.MentionNameAlias)
	}
	if got := getEffectiveBotOpenID(cfg).Value; got != "ou_bot" {
		t.Fatalf("effective bot open id = %q, want ou_bot", got)
	}

	second := prepareMessageEnvelope(cfg, buildTestEvent("oc_group", "group", "om_mention_2", "image", `{"image_key":"img-1"}`, "ou_sender"), store)
	if second == nil {
		t.Fatalf("second envelope is nil")
	}
	if !second.Meta.AllowMentionCarry {
		t.Fatalf("expected mention carry after autodetected explicit mention")
	}
}

func TestDetectBotOpenIDCandidate(t *testing.T) {
	candidate := detectBotOpenIDCandidate([]*larkim.MentionEvent{
		{
			Name: stringPtr("助手"),
			Id: &larkim.UserId{
				OpenId: stringPtr("ou_bot"),
			},
		},
	}, []string{"助手", "AI 助手"})
	if candidate == nil {
		t.Fatalf("expected candidate")
	}
	if candidate.OpenID != "ou_bot" {
		t.Fatalf("open id = %q", candidate.OpenID)
	}
}

func TestReconcileBotOpenIDFromMentionsPersistsOverlay(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "config", "feishu"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		RepoRoot:       repo,
		AccountName:    "assistant",
		MentionAliases: []string{"助手"},
	}
	globalRuntimeBotOpenIDs = &runtimeBotOpenIDStore{values: map[string]ResolvedValue{}}

	candidate := reconcileBotOpenIDFromMentions(cfg, []*larkim.MentionEvent{
		{
			Name: stringPtr("助手"),
			Id: &larkim.UserId{
				OpenId: stringPtr("ou_bot"),
			},
		},
	})
	if candidate == nil {
		t.Fatalf("expected candidate")
	}
	effective := getEffectiveBotOpenID(cfg)
	if effective.Value != "ou_bot" {
		t.Fatalf("effective bot open id = %q", effective.Value)
	}
	body, err := os.ReadFile(filepath.Join(repo, "config", "feishu", "bots.toml"))
	if err != nil {
		t.Fatalf("read bots.toml: %v", err)
	}
	if !strings.Contains(string(body), "bot_open_id = \"ou_bot\"") {
		t.Fatalf("bots.toml missing bot_open_id: %s", string(body))
	}
}

func TestRecentMentionStoreExpires(t *testing.T) {
	store := newRecentMentionStore()
	now := time.Now()
	store.remember("oc_group", "ou_sender", "助手", now.Add(-groupMentionCarryWindow-time.Second))
	if state := store.get("oc_group", "ou_sender", now); state != nil {
		t.Fatalf("expected expired mention state to be pruned")
	}
}

func TestMessageDispatcherSupersedesActiveTask(t *testing.T) {
	started := make(chan string, 4)
	cancelled := make(chan string, 1)

	dispatcher := newMessageDispatcher(context.Background(), func(ctx context.Context, envelope *messageEnvelope, task *chatTaskControl) error {
		messageID := deref(envelope.Event.Event.Message.MessageId)
		started <- messageID
		if messageID == "om_1" {
			task.OnCancel(func(reason string) {
				cancelled <- reason
			})
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})

	dispatcher.Dispatch(&messageEnvelope{
		Event:                     buildTestEvent("oc_group", "group", "om_1", "text", `{"text":"@助手 第一条"}`, "ou_sender"),
		Scope:                     conversationScope{TaskKey: "oc_group::ou_sender", StateKey: "oc_group::ou_sender", Kind: "group_sender"},
		Meta:                      dispatchMeta{ExplicitBotMention: true},
		ShouldSupersedeActiveTask: true,
	})
	waitForString(t, started, "om_1")

	dispatcher.Dispatch(&messageEnvelope{
		Event:                     buildTestEvent("oc_group", "group", "om_2", "text", `{"text":"@助手 第二条"}`, "ou_sender"),
		Scope:                     conversationScope{TaskKey: "oc_group::ou_sender", StateKey: "oc_group::ou_sender", Kind: "group_sender"},
		Meta:                      dispatchMeta{ExplicitBotMention: true},
		ShouldSupersedeActiveTask: true,
	})

	waitForString(t, cancelled, "superseded_by_new_message")
	waitForString(t, started, "om_2")
}

func TestBuildCodexExecArgsResume(t *testing.T) {
	args := buildCodexExecArgs(CodexConfig{
		Model:           "gpt-5.2",
		ReasoningEffort: "high",
		Profile:         "default",
		Cwd:             "/workspace/assistant",
		AddDirs:         []string{"/workspace/shared"},
		Sandbox:         "danger-full-access",
		ApprovalPolicy:  "never",
	}, "/tmp/out.txt", []string{"/tmp/img.png"}, "thread_123")

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "exec resume") {
		t.Fatalf("resume args missing exec resume: %v", args)
	}
	if strings.Contains(joined, "-p default") || strings.Contains(joined, "-C /workspace/assistant") || strings.Contains(joined, "--add-dir /workspace/shared") {
		t.Fatalf("resume args should not include fresh-session workspace flags: %v", args)
	}
	if !strings.Contains(joined, "-i /tmp/img.png") {
		t.Fatalf("resume args should include image path: %v", args)
	}
	if !strings.Contains(joined, "thread_123 -") {
		t.Fatalf("resume args should include thread id before stdin marker: %v", args)
	}
}

func TestCodexThreadPolicyAndTitleSync(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not found")
	}

	codexHome := t.TempDir()
	dbPath := filepath.Join(codexHome, "state_1.sqlite")
	sql := strings.Join([]string{
		"CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT, sandbox_policy TEXT, approval_mode TEXT, updated_at INTEGER);",
		"INSERT INTO threads (id, title, sandbox_policy, approval_mode, updated_at) VALUES ('thread_1', '旧标题', '{\"type\":\"danger-full-access\"}', 'never', 0);",
	}, " ")
	if out, err := exec.Command("sqlite3", dbPath, sql).CombinedOutput(); err != nil {
		t.Fatalf("init sqlite db: %v output=%s", err, string(out))
	}

	t.Setenv("CODEX_HOME", codexHome)
	cachedCodexStateDBPath = ""

	policy := readCodexThreadExecutionPolicy("thread_1")
	if policy == nil {
		t.Fatalf("expected policy")
	}
	if policy.SandboxType != "danger-full-access" || policy.ApprovalMode != "never" {
		t.Fatalf("unexpected policy: %+v", policy)
	}

	if !syncCodexThreadTitle("thread_1", "新标题") {
		t.Fatalf("expected title sync to succeed")
	}
	out, err := exec.Command("sqlite3", dbPath, "SELECT title FROM threads WHERE id = 'thread_1';").CombinedOutput()
	if err != nil {
		t.Fatalf("query updated title: %v output=%s", err, string(out))
	}
	if got := strings.TrimSpace(string(out)); got != "新标题" {
		t.Fatalf("title = %q, want 新标题", got)
	}
}

func buildTestEvent(chatID, chatType, messageID, messageType, rawContent, senderOpenID string) *larkim.P2MessageReceiveV1 {
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				ChatId:      stringPtr(chatID),
				ChatType:    stringPtr(chatType),
				MessageId:   stringPtr(messageID),
				MessageType: stringPtr(messageType),
				Content:     stringPtr(rawContent),
			},
			Sender: &larkim.EventSender{
				SenderType: stringPtr("user"),
				SenderId: &larkim.UserId{
					OpenId: stringPtr(senderOpenID),
				},
			},
		},
	}
}

func stringPtr(value string) *string {
	return &value
}

func waitForString(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for %q", want)
	}
}

func quote(value string) string {
	return "\"" + strings.ReplaceAll(value, "\\", "\\\\") + "\""
}
