package feishunative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type RunOptions struct {
	RepoRoot      string
	Account       string
	DryRun        bool
	TimerTaskFile string
	Stdout        io.Writer
	Stderr        io.Writer
}

var runCodexFunc = RunCodex

var sendTextReplyFunc = sendTextReply

func Run(ctx context.Context, opts RunOptions) error {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	cfg, err := Load(opts.RepoRoot, opts.Account)
	if err != nil {
		return err
	}
	codexFound, codexVersion := DetectBinary(cfg.Codex.Bin, "--version")

	if opts.DryRun {
		return writeDryRun(opts.Stdout, cfg, codexFound, codexVersion)
	}

	if cfg.AppID.Value == "" {
		return fmt.Errorf("feishu app_id not found for account %q", cfg.AccountName)
	}
	if cfg.AppSecret.Value == "" {
		return fmt.Errorf("feishu app_secret not found for account %q", cfg.AccountName)
	}
	if (cfg.ReplyMode == "codex" || strings.TrimSpace(opts.TimerTaskFile) != "") && !codexFound {
		return fmt.Errorf("codex binary not found: %s", cfg.Codex.Bin)
	}

	initResult, err := EnsureRuntimeWorkspace(opts.RepoRoot, cfg)
	if err != nil {
		return err
	}
	writeRuntimeInitLogs(opts.Stdout, cfg, initResult)

	client := lark.NewClient(
		cfg.AppID.Value,
		cfg.AppSecret.Value,
		lark.WithOpenBaseUrl(cfg.DomainBaseURL),
	)

	if strings.TrimSpace(opts.TimerTaskFile) != "" {
		return runTimerTask(ctx, client, cfg, strings.TrimSpace(opts.TimerTaskFile), opts.Stdout)
	}

	writeStartupLogs(opts.Stdout, cfg, codexVersion)
	mentionStore := newRecentMentionStore()
	messageDispatcher := newMessageDispatcher(ctx, func(taskCtx context.Context, envelope *messageEnvelope, task *chatTaskControl) error {
		return handleMessageEvent(taskCtx, client, cfg, envelope, task)
	})
	dispatcher := larkdispatcher.NewEventDispatcher("", "").OnP2MessageReceiveV1(func(_ context.Context, event *larkim.P2MessageReceiveV1) error {
		envelope := prepareMessageEnvelope(cfg, event, mentionStore)
		if envelope != nil {
			messageDispatcher.Dispatch(envelope)
		}
		return nil
	})
	wsClient := larkws.NewClient(
		cfg.AppID.Value,
		cfg.AppSecret.Value,
		larkws.WithEventHandler(dispatcher),
		larkws.WithDomain(cfg.DomainBaseURL),
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- wsClient.Start(ctx)
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func writeDryRun(w io.Writer, cfg Config, codexFound bool, codexVersion string) error {
	_, err := fmt.Fprintf(w, "FEISHU_WS_DRY_RUN\naccount=%s\n", cfg.AccountName)
	if err != nil {
		return err
	}
	if cfg.ConfigPath != "" {
		_, _ = fmt.Fprintf(w, "config=%s\n", cfg.ConfigPath)
	}
	lines := []string{
		"domain=" + cfg.DomainLabel,
		"app_id_found=" + boolText(cfg.AppID.Value != ""),
		"app_secret_found=" + boolText(cfg.AppSecret.Value != ""),
		"encrypt_key_found=" + boolText(cfg.EncryptKey.Value != ""),
		"verification_token_found=" + boolText(cfg.VerificationToken.Value != ""),
		"bot_open_id_found=" + boolText(cfg.BotOpenID.Value != ""),
		"bot_open_id_autodetect=" + ternary(cfg.RequireMention, "enabled", "not_needed"),
		"auto_reply=" + boolText(cfg.AutoReply),
		"ignore_self=" + boolText(cfg.IgnoreSelf),
		"bot_name=" + emptyFallback(cfg.BotName, "(none)"),
		"require_mention=" + boolText(cfg.RequireMention),
		"require_mention_group_only=" + boolText(cfg.RequireMentionGroupOnly),
		"mention_aliases=" + emptyFallback(strings.Join(cfg.MentionAliases, " | "), "(none)"),
		"reply_mode=" + cfg.ReplyMode,
		"reply_prefix=" + cfg.ReplyPrefix,
		"typing_indicator=" + boolText(cfg.TypingIndicatorEnabled),
		"typing_emoji=" + emptyFallback(cfg.TypingEmoji, "Typing"),
		"fake_stream=" + boolText(cfg.FakeStreamEnabled),
		fmt.Sprintf("fake_stream_interval_ms=%d", cfg.FakeStreamIntervalMS),
		fmt.Sprintf("fake_stream_chunk_chars=%d", cfg.FakeStreamChunkChars),
		fmt.Sprintf("fake_stream_max_updates=%d", cfg.FakeStreamMaxUpdates),
		"progress_notice=" + boolText(cfg.Progress.Enabled),
		"progress_message=" + cfg.Progress.Message,
		"progress_mode=" + cfg.Progress.Mode,
		"codex_bin=" + cfg.Codex.Bin,
		"codex_found=" + boolText(codexFound),
		"codex_api_key_found=" + boolText(cfg.Codex.APIKey != ""),
		"codex_model=" + emptyFallback(cfg.Codex.Model, "(default)"),
		"codex_reasoning_effort=" + emptyFallback(cfg.Codex.ReasoningEffort, "(default)"),
		"codex_profile=" + emptyFallback(cfg.Codex.Profile, "(default)"),
		"codex_cwd=" + emptyFallback(cfg.Codex.Cwd, optsFallbackCwd()),
		"codex_add_dirs=" + emptyFallback(strings.Join(cfg.Codex.AddDirs, " | "), "(none)"),
		"codex_sandbox=" + cfg.Codex.Sandbox,
		"codex_approval_policy=" + cfg.Codex.ApprovalPolicy,
		"codex_bypass_sandbox=" + boolText(shouldBypassSandbox(cfg.Codex.Sandbox, cfg.Codex.ApprovalPolicy)),
		"codex_timeout_sec=(disabled)",
		fmt.Sprintf("codex_history_turns=%d", cfg.Codex.HistoryTurns),
		"speech_enabled=" + boolText(cfg.Speech.Enabled),
		"speech_api_key_found=" + boolText(cfg.Speech.APIKey != ""),
		"speech_model=" + cfg.Speech.Model,
		"speech_language=" + emptyFallback(cfg.Speech.Language, "(auto)"),
		"speech_base_url=" + cfg.Speech.BaseURL,
		"speech_ffmpeg_bin=" + emptyFallback(cfg.Speech.FFmpegBin, "(not found)"),
	}
	if cfg.Progress.Mode == "doc" {
		lines = append(lines,
			"progress_doc_title_prefix="+cfg.Progress.Doc.TitlePrefix,
			"progress_doc_share_to_chat="+boolText(cfg.Progress.Doc.ShareToChat),
			"progress_doc_link_scope="+cfg.Progress.Doc.LinkScope,
			"progress_doc_include_user_message="+boolText(cfg.Progress.Doc.IncludeUserMessage),
			"progress_doc_write_final_reply="+boolText(cfg.Progress.Doc.WriteFinalReply),
		)
	}
	if codexVersion != "" {
		lines = append(lines, "codex_version="+codexVersion)
	}
	if cfg.AppID.Source != "" {
		lines = append(lines, "app_id_source="+cfg.AppID.Source)
	}
	if cfg.AppSecret.Source != "" {
		lines = append(lines, "app_secret_source="+cfg.AppSecret.Source)
	}
	if cfg.EncryptKey.Source != "" {
		lines = append(lines, "encrypt_key_source="+cfg.EncryptKey.Source)
	}
	if cfg.VerificationToken.Source != "" {
		lines = append(lines, "verification_token_source="+cfg.VerificationToken.Source)
	}
	if cfg.BotOpenID.Source != "" {
		lines = append(lines, "bot_open_id_source="+cfg.BotOpenID.Source)
	}
	if cfg.Codex.APIKeySource != "" {
		lines = append(lines, "codex_api_key_source="+cfg.Codex.APIKeySource)
	}
	if cfg.Speech.APIKeySource != "" {
		lines = append(lines, "speech_api_key_source="+cfg.Speech.APIKeySource)
	}
	if cfg.Speech.FFmpegVersion != "" {
		lines = append(lines, "speech_ffmpeg_version="+cfg.Speech.FFmpegVersion)
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func writeRuntimeInitLogs(w io.Writer, cfg Config, initResult WorkspaceInitResult) {
	if initResult.ConfigWritten && initResult.ConfigPath != "" {
		fmt.Fprintf(w, "runtime_config_toml=%s\n", initResult.ConfigPath)
	}
	if initResult.GitInitialized {
		fmt.Fprintf(w, "runtime_git_initialized=%s\n", cfg.Codex.Cwd)
	}
	if initResult.RestoreAttempted {
		if initResult.RestoreSucceeded {
			fmt.Fprintf(w, "runtime_sync_restore=ok account=%s docs=%s message=%s\n", cfg.AccountName, emptyFallback(strings.Join(initResult.RestoredDocs, " | "), "(none)"), emptyFallback(initResult.RestoreOutput, "ok"))
		} else {
			fmt.Fprintf(w, "runtime_sync_restore=skip account=%s message=%s\n", cfg.AccountName, emptyFallback(initResult.RestoreOutput, "not_restored"))
		}
	}
	if len(initResult.CreatedDocs) > 0 {
		fmt.Fprintf(w, "runtime_docs_initialized=%s\n", strings.Join(initResult.CreatedDocs, " | "))
	}
	if initResult.DefaultSyncTaskCreated && initResult.DefaultSyncTaskPath != "" {
		fmt.Fprintf(w, "runtime_default_timer_created=%s path=%s\n", initResult.DefaultSyncTaskID, initResult.DefaultSyncTaskPath)
	}
}

func writeStartupLogs(w io.Writer, cfg Config, codexVersion string) {
	fmt.Fprintln(w, "FEISHU_WS_BOT_RUNNING")
	fmt.Fprintf(w, "account=%s\n", cfg.AccountName)
	if cfg.ConfigPath != "" {
		fmt.Fprintf(w, "config=%s\n", cfg.ConfigPath)
	}
	lines := []string{
		"domain=" + cfg.DomainLabel,
		"auto_reply=" + boolText(cfg.AutoReply),
		"ignore_self=" + boolText(cfg.IgnoreSelf),
		"bot_name=" + emptyFallback(cfg.BotName, "(none)"),
		"require_mention=" + boolText(cfg.RequireMention),
		"require_mention_group_only=" + boolText(cfg.RequireMentionGroupOnly),
		"bot_open_id_autodetect=" + ternary(cfg.RequireMention, "enabled", "not_needed"),
		"mention_aliases=" + emptyFallback(strings.Join(cfg.MentionAliases, " | "), "(none)"),
		"reply_mode=" + cfg.ReplyMode,
		"typing_indicator=" + boolText(cfg.TypingIndicatorEnabled),
		"fake_stream=" + boolText(cfg.FakeStreamEnabled),
		"progress_notice=" + boolText(cfg.Progress.Enabled),
		"progress_mode=" + cfg.Progress.Mode,
		"codex_bin=" + cfg.Codex.Bin,
		"codex_model=" + emptyFallback(cfg.Codex.Model, "(default)"),
		"codex_reasoning_effort=" + emptyFallback(cfg.Codex.ReasoningEffort, "(default)"),
		"codex_profile=" + emptyFallback(cfg.Codex.Profile, "(default)"),
		"codex_cwd=" + emptyFallback(cfg.Codex.Cwd, optsFallbackCwd()),
		"codex_add_dirs=" + emptyFallback(strings.Join(cfg.Codex.AddDirs, " | "), "(none)"),
		"codex_sandbox=" + cfg.Codex.Sandbox,
		"codex_approval_policy=" + cfg.Codex.ApprovalPolicy,
		"codex_bypass_sandbox=" + boolText(shouldBypassSandbox(cfg.Codex.Sandbox, cfg.Codex.ApprovalPolicy)),
		"codex_timeout_sec=(disabled)",
		fmt.Sprintf("codex_history_turns=%d", cfg.Codex.HistoryTurns),
		"speech_enabled=" + boolText(cfg.Speech.Enabled),
		"speech_model=" + cfg.Speech.Model,
		"speech_language=" + emptyFallback(cfg.Speech.Language, "(auto)"),
		"speech_base_url=" + cfg.Speech.BaseURL,
		"speech_api_key_found=" + boolText(cfg.Speech.APIKey != ""),
		"speech_api_key_source=" + emptyFallback(cfg.Speech.APIKeySource, "config"),
		"speech_ffmpeg_bin=" + emptyFallback(cfg.Speech.FFmpegBin, "(not found)"),
	}
	if cfg.Progress.Mode == "doc" {
		lines = append(lines,
			"progress_doc_title_prefix="+cfg.Progress.Doc.TitlePrefix,
			"progress_doc_share_to_chat="+boolText(cfg.Progress.Doc.ShareToChat),
			"progress_doc_link_scope="+cfg.Progress.Doc.LinkScope,
			"progress_doc_include_user_message="+boolText(cfg.Progress.Doc.IncludeUserMessage),
			"progress_doc_write_final_reply="+boolText(cfg.Progress.Doc.WriteFinalReply),
		)
	}
	if codexVersion != "" {
		lines = append(lines, "codex_version="+codexVersion)
	}
	if cfg.Speech.FFmpegVersion != "" {
		lines = append(lines, "speech_ffmpeg_version="+cfg.Speech.FFmpegVersion)
	}
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
}

func handleMessageEvent(ctx context.Context, client *lark.Client, cfg Config, envelope *messageEnvelope, task *chatTaskControl) error {
	if envelope == nil || envelope.Event == nil || envelope.Event.Event == nil || envelope.Event.Event.Message == nil {
		return nil
	}
	message := envelope.Event.Event.Message
	chatID := deref(message.ChatId)
	messageID := deref(message.MessageId)
	messageType := strings.TrimSpace(deref(message.MessageType))
	chatType := strings.TrimSpace(deref(message.ChatType))
	rawContent := deref(message.Content)
	senderOpenID := ""
	senderType := ""
	if envelope.Event.Event.Sender != nil {
		senderType = deref(envelope.Event.Event.Sender.SenderType)
		if envelope.Event.Event.Sender.SenderId != nil {
			senderOpenID = deref(envelope.Event.Event.Sender.SenderId.OpenId)
		}
	}
	fmt.Println("FEISHU_EVENT")
	fmt.Println("event=im.message.receive_v1")
	fmt.Printf("chat_id=%s\n", emptyFallback(chatID, "(unknown)"))
	fmt.Printf("chat_type=%s\n", emptyFallback(chatType, "(unknown)"))
	if len(message.Mentions) > 0 {
		_ = reconcileBotOpenIDFromMentions(cfg, message.Mentions)
	}
	if envelope.Scope.TaskKey != "" {
		fmt.Printf("chat_scope=%s\n", envelope.Scope.TaskKey)
	}
	if envelope.Scope.Kind != "" {
		fmt.Printf("chat_scope_kind=%s\n", envelope.Scope.Kind)
	}
	fmt.Printf("message_id=%s\n", emptyFallback(messageID, "(unknown)"))
	fmt.Printf("message_type=%s\n", emptyFallback(messageType, "(unknown)"))
	fmt.Printf("sender_type=%s\n", emptyFallback(senderType, "(unknown)"))
	if envelope.Meta.MentionNameAlias != "" {
		fmt.Printf("mention_fallback=mention_name alias=%s\n", envelope.Meta.MentionNameAlias)
	}
	if envelope.Meta.TextMentionAlias != "" {
		fmt.Printf("mention_fallback=text_alias alias=%s\n", envelope.Meta.TextMentionAlias)
	}
	if envelope.Meta.AllowMentionCarry {
		if envelope.Meta.CarryAge > 0 {
			fmt.Printf("mention_fallback=recent_sender_window age_ms=%d\n", envelope.Meta.CarryAge.Milliseconds())
		} else if envelope.Meta.QueuedCarry {
			fmt.Println("mention_fallback=queued_sender_window")
		} else {
			fmt.Println("mention_fallback=recent_sender_window")
		}
	}
	parsedPost := incomingPost{}
	if strings.EqualFold(messageType, "post") {
		parsedPost = parsePostMessageContent(rawContent)
	}
	tempCleanup := []string{}
	defer func() {
		for _, path := range tempCleanup {
			_ = os.RemoveAll(path)
		}
	}()

	text, textualMention := parseIncomingText(messageType, rawContent)
	textualMention = detectTextualBotMention(text, cfg.MentionAliases)
	text = normalizeIncomingText(text, message.Mentions, cfg.MentionAliases)
	userText := text
	historyUserText := text
	var imagePaths []string
	if strings.TrimSpace(chatID) == "" {
		fmt.Println("skip_reason=missing_chat_id")
		return nil
	}
	if cfg.IgnoreSelf && strings.TrimSpace(senderType) != "" && !strings.EqualFold(strings.TrimSpace(senderType), "user") {
		fmt.Println("skip_reason=non_user_sender")
		return nil
	}
	if effective := getEffectiveBotOpenID(cfg); cfg.IgnoreSelf && strings.TrimSpace(senderOpenID) != "" && strings.TrimSpace(effective.Value) != "" && strings.TrimSpace(senderOpenID) == strings.TrimSpace(effective.Value) {
		fmt.Println("skip_reason=self_open_id")
		return nil
	}
	if !isSupportedIncomingMessageType(messageType) {
		fmt.Printf("skip_reason=unsupported_message_type type=%s\n", emptyFallback(messageType, "(empty)"))
		return nil
	}
	switch strings.ToLower(messageType) {
	case "file":
		parsed := parseFileMessageContent(rawContent)
		if strings.TrimSpace(parsed.FileKey) == "" {
			fmt.Println("skip_reason=missing_file_key")
			return sendTextReply(ctx, client, chatID, "文件接收失败，请重新发送。")
		}
		tempDir, filePath, fileName, err := downloadFileToTempFile(ctx, client, messageID, parsed.FileKey, parsed.FileName)
		if err != nil {
			return sendTextReply(ctx, client, chatID, "文件下载失败，请稍后重试。")
		}
		tempCleanup = append(tempCleanup, tempDir)
		lines := []string{}
		if strings.TrimSpace(text) != "" {
			lines = append(lines, "用户发送了 1 个文件，并附带文字："+strings.TrimSpace(text))
		} else {
			lines = append(lines, "用户发送了 1 个文件，请先读取文件内容再回答。")
		}
		lines = append(lines, "文件名："+fileName)
		if parsed.FileSize > 0 {
			lines = append(lines, "文件大小："+formatBytes(parsed.FileSize))
		}
		lines = append(lines, "本地临时路径："+filePath, "如需使用文件内容，请直接读取该本地文件。")
		userText = strings.Join(lines, "\n")
		if strings.TrimSpace(text) != "" {
			historyUserText = "[文件消息] " + fileName + " + 文本：" + strings.TrimSpace(text)
		} else {
			historyUserText = "[文件消息] " + fileName
		}
	case "image":
		parsed := parseImageMessageContent(rawContent)
		if len(parsed.ImageKeys) == 0 {
			fmt.Println("skip_reason=missing_image_key")
			return sendTextReply(ctx, client, chatID, "图片接收失败，请稍后重试。")
		}
		imageUserText, imageHistoryText, nextImagePaths, cleanupDirs, err := buildIncomingImagePrompt(ctx, client, messageID, parsed.ImageKeys, text)
		if err != nil {
			return sendTextReply(ctx, client, chatID, "图片接收失败，请稍后重试。")
		}
		tempCleanup = append(tempCleanup, cleanupDirs...)
		imagePaths = append(imagePaths, nextImagePaths...)
		userText = imageUserText
		historyUserText = imageHistoryText
	case "audio":
		parsed := parseAudioMessageContent(rawContent)
		if strings.TrimSpace(parsed.FileKey) == "" {
			fmt.Println("skip_reason=missing_audio_key")
			return sendTextReply(ctx, client, chatID, "语音接收失败，请重新发送。")
		}
		tempDir, filePath, _, err := downloadAudioToTempFile(ctx, client, messageID, parsed.FileKey)
		if err != nil {
			return sendTextReply(ctx, client, chatID, "语音下载失败，请稍后重试。")
		}
		tempCleanup = append(tempCleanup, tempDir)
		transcript, err := transcribeAudioMessage(ctx, filePath, cfg.Speech)
		if err != nil {
			if strings.TrimSpace(cfg.Speech.FFmpegBin) == "" && strings.Contains(strings.ToLower(err.Error()), "ffmpeg") {
				return sendTextReply(ctx, client, chatID, "当前环境缺少语音转码能力，请安装 ffmpeg 或使用带 ffmpeg 的镜像后重试。")
			}
			return sendTextReply(ctx, client, chatID, "语音转写失败，请检查 speech.api_key / 网络后重试。")
		}
		durationText := formatDurationFromMS(parsed.DurationMS)
		lines := []string{}
		if durationText != "" {
			lines = append(lines, "用户发送了 1 条语音消息，时长约 "+durationText+"。")
		} else {
			lines = append(lines, "用户发送了 1 条语音消息。")
		}
		lines = append(lines,
			"下面是语音转写结果（可能存在少量识别误差）：",
			strings.TrimSpace(transcript.Text),
			"请基于语音内容直接回答用户。",
		)
		userText = strings.Join(lines, "\n")
		historyUserText = compactText(
			fmt.Sprintf("[语音消息%s] %s", ternary(durationText != "", " "+durationText, ""), strings.TrimSpace(transcript.Text)),
			4000,
		)
	case "post":
		if len(parsedPost.ImageKeys) > 0 {
			imageUserText, imageHistoryText, nextImagePaths, cleanupDirs, err := buildIncomingImagePrompt(ctx, client, messageID, parsedPost.ImageKeys, parsedPost.Text)
			if err != nil {
				return sendTextReply(ctx, client, chatID, "图片接收失败，请稍后重试。")
			}
			tempCleanup = append(tempCleanup, cleanupDirs...)
			imagePaths = append(imagePaths, nextImagePaths...)
			userText = imageUserText
			historyUserText = imageHistoryText
		}
	}
	if strings.TrimSpace(userText) == "" {
		fmt.Println("skip_reason=empty_text")
		return nil
	}
	chatStateKey := emptyFallback(envelope.Scope.StateKey, emptyFallback(chatID, "default"))
	chatState := ensureChatState(chatStateKey)
	if messageType == "text" {
		for _, command := range []*adminCommand{
			parseSyncCommand(userText),
			parseMemoryCommand(userText),
			parseTimerCommand(userText),
		} {
			handled, reply, err := handleAdminCommand(ctx, cfg, chatID, command)
			if !handled {
				continue
			}
			if err != nil {
				prefix := "命令执行失败"
				switch command.Kind {
				case "sync":
					prefix = "同步命令执行失败"
				case "memory":
					prefix = "记忆命令执行失败"
				case "timer":
					prefix = "定时任务命令执行失败"
				}
				fmt.Printf("reply=error mode=%s_command type=%s message=%s\n", command.Kind, command.Action, compactText(err.Error(), 400))
				_ = sendTextReply(ctx, client, chatID, prefix+"："+err.Error())
				return nil
			}
			if strings.TrimSpace(reply) == "" {
				reply = "ok"
			}
			fmt.Printf("reply=ok mode=%s_command type=%s\n", command.Kind, command.Action)
			return sendTextReply(ctx, client, chatID, reply)
		}
		if threadCommand := parseThreadCommand(userText); threadCommand != nil {
			chatState.mu.Lock()
			handled, reply := handleThreadCommand(chatState, threadCommand)
			currentThreadID := chatState.CurrentThreadID
			totalThreads := len(chatState.Order)
			chatState.mu.Unlock()
			if handled {
				fmt.Printf("thread_state total=%d current=%s\n", totalThreads, currentThreadID)
				fmt.Printf("reply=ok mode=thread_command thread=%s\n", currentThreadID)
				return sendTextReply(ctx, client, chatID, reply)
			}
		}
		if isResetCommand(userText) {
			chatState.mu.Lock()
			reply, ok := resetCurrentThread(chatState)
			currentThreadID := chatState.CurrentThreadID
			chatState.mu.Unlock()
			if !ok {
				fmt.Println("reply=ok mode=reset_missing_thread")
				return sendTextReply(ctx, client, chatID, "当前线程不存在，请先用 /thread new 创建。")
			}
			fmt.Printf("reply=ok mode=reset thread=%s\n", emptyFallback(currentThreadID, "(none)"))
			return sendTextReply(ctx, client, chatID, reply)
		}
	}
	if !cfg.AutoReply {
		fmt.Println("skip_reason=auto_reply_disabled")
		return nil
	}
	if !shouldReply(cfg, message, chatType, userText, textualMention, envelope.Meta.AllowMentionCarry) {
		fmt.Println("skip_reason=require_mention_not_met")
		fmt.Printf("mention_count=%d\n", len(message.Mentions))
		fmt.Printf("text_has_at=%s\n", boolText(strings.ContainsAny(text, "@＠")))
		return nil
	}
	userText = normalizeIncomingText(userText, message.Mentions, cfg.MentionAliases)
	if strings.TrimSpace(userText) == "" {
		fmt.Println("skip_reason=empty_text_after_strip")
		return nil
	}

	var progress progressReporter
	var typingState *typingIndicatorState
	if cfg.ReplyMode == "codex" {
		progress = startProgressReporter(ctx, client, chatID, cfg, userText)
		if cfg.TypingIndicatorEnabled {
			typingState = addTypingIndicatorSafe(ctx, client, messageID, cfg.TypingEmoji)
		}
		if task != nil {
			task.OnCancel(func(string) {
				if progress != nil {
					progress.abort(context.Background(), "当前任务已被同一会话中的新消息接管。")
				}
				removeTypingIndicatorSafe(context.Background(), client, typingState)
			})
		}
		defer removeTypingIndicatorSafe(context.Background(), client, typingState)
	}

	var reply string
	var codexReply CodexReply
	var history []historyEntry
	var threadName string
	var activeThreadID string
	var activeCodexThreadID string
	var err error
	chatState.mu.Lock()
	currentThread := getCurrentThread(chatState)
	if currentThread == nil {
		currentThread = makeThread("t1", "主线程")
		chatState.Threads[currentThread.ID] = currentThread
		chatState.Order = []string{currentThread.ID}
		chatState.CurrentThreadID = currentThread.ID
		if chatState.NextThreadSeq <= 1 {
			chatState.NextThreadSeq = 2
		}
	}
	history = append([]historyEntry(nil), currentThread.History...)
	threadName = currentThread.Name
	activeThreadID = currentThread.ID
	activeCodexThreadID = currentThread.CodexThreadID
	chatState.mu.Unlock()

	switch cfg.ReplyMode {
	case "echo":
		reply = userText
	default:
		if task != nil {
			if err := task.ThrowIfCancelled(); err != nil {
				return nil
			}
		}
		codexThreadTitle := buildCodexThreadTitle(cfg, threadName, historyUserText)
		freshPrompt := buildPrompt(cfg, chatID, userText, codexThreadTitle, history)
		codexPrompt := freshPrompt
		fallbackPrompt := ""
		if strings.TrimSpace(activeCodexThreadID) != "" {
			codexPrompt = buildResumePrompt(cfg, chatID, userText, len(imagePaths))
			fallbackPrompt = freshPrompt
		}
		codexReply, err = runCodexFunc(ctx, cfg.Codex, CodexRunRequest{
			Prompt:          codexPrompt,
			FallbackPrompt:  fallbackPrompt,
			ResumeSessionID: activeCodexThreadID,
			ImagePaths:      imagePaths,
			OnEvent: func(event codexProgressEvent) {
				if progress != nil {
					progress.recordEvent(ctx, event)
				}
			},
		})
		if err != nil {
			if isCancelledError(err) {
				clearThreadCodexSession(chatState, activeThreadID)
				fmt.Printf("reply=cancelled mode=%s reason=%s\n", cfg.ReplyMode, cancelReason(task, err))
				return nil
			}
			if progress != nil {
				progress.fail(ctx, buildCodexExecutionFailureReply(err))
			}
			_ = sendTextReplyFunc(ctx, client, chatID, buildCodexExecutionFailureReply(err))
			fmt.Printf("reply=error mode=%s message=%s\n", cfg.ReplyMode, compactText(err.Error(), 400))
			return err
		}
		reply = codexReply.Reply
		if strings.TrimSpace(codexReply.ThreadID) != "" {
			chatState.mu.Lock()
			if currentThread := chatState.Threads[activeThreadID]; currentThread != nil {
				currentThread.CodexThreadID = codexReply.ThreadID
				currentThread.UpdatedAt = time.Now().UnixMilli()
			}
			chatState.mu.Unlock()
			synced := syncCodexThreadTitle(codexReply.ThreadID, codexThreadTitle)
			fmt.Printf("codex_thread_title_sync=%s thread_id=%s\n", ternary(synced, "ok", "skip"), codexReply.ThreadID)
			fmt.Printf("codex_thread_id=%s chat_id=%s bot=%q local_thread=%s\n", codexReply.ThreadID, chatID, emptyFallback(cfg.BotName, cfg.AccountName), activeThreadID)
		}
	}
	reply, sendErr := sendReplyWithAttachments(ctx, client, chatID, reply, emptyFallback(cfg.Codex.Cwd, optsFallbackCwd()), cfg)
	if sendErr != nil {
		if isCancelledError(sendErr) {
			clearThreadCodexSession(chatState, activeThreadID)
			fmt.Printf("reply=cancelled mode=%s reason=%s\n", cfg.ReplyMode, cancelReason(task, sendErr))
			return nil
		}
		if progress != nil {
			progress.fail(ctx, "处理失败："+sendErr.Error())
		}
		fmt.Printf("reply=error mode=%s message=%s\n", cfg.ReplyMode, compactText(sendErr.Error(), 400))
		if messageID != "" {
			_ = sendTextReply(ctx, client, chatID, "处理消息失败："+sendErr.Error())
		}
		return sendErr
	}
	finishProgressReporter(ctx, progress, "执行完成，回复见下条消息。", reply)
	chatState.mu.Lock()
	if currentThread := chatState.Threads[activeThreadID]; currentThread != nil {
		appendHistory(currentThread, "user", historyUserText, cfg.Codex.HistoryTurns)
		appendHistory(currentThread, "assistant", reply, cfg.Codex.HistoryTurns)
	}
	chatState.mu.Unlock()
	fmt.Printf("reply=ok mode=%s thread=%s\n", cfg.ReplyMode, activeThreadID)
	return nil
}

func isSupportedIncomingMessageType(messageType string) bool {
	switch strings.ToLower(strings.TrimSpace(messageType)) {
	case "text", "image", "post", "file", "audio":
		return true
	default:
		return false
	}
}

type timerTask struct {
	ID              string   `json:"id"`
	Prompt          string   `json:"prompt"`
	ChatID          string   `json:"chat_id"`
	Cwd             string   `json:"cwd"`
	AddDirs         []string `json:"add_dirs"`
	Model           string   `json:"model"`
	ReasoningEffort string   `json:"reasoning_effort"`
}

func runTimerTask(ctx context.Context, client *lark.Client, cfg Config, taskFile string, w io.Writer) error {
	body, err := os.ReadFile(taskFile)
	if err != nil {
		return err
	}
	var task timerTask
	if err := json.Unmarshal(body, &task); err != nil {
		return err
	}
	task.ID = emptyFallback(task.ID, strings.TrimSuffix(filepath.Base(taskFile), filepath.Ext(taskFile)))
	task.Prompt = strings.TrimSpace(task.Prompt)
	task.ChatID = strings.TrimSpace(task.ChatID)
	if task.Prompt == "" {
		return fmt.Errorf("timer task prompt is empty: %s", taskFile)
	}
	if task.ChatID == "" {
		return fmt.Errorf("timer task chat_id is empty: %s", taskFile)
	}
	timerCodex := cfg.Codex
	if resolved := resolveOptionalDir(task.Cwd); resolved != "" {
		timerCodex.Cwd = resolved
	}
	if len(task.AddDirs) > 0 {
		timerCodex.AddDirs = task.AddDirs
	}
	if strings.TrimSpace(task.Model) != "" {
		timerCodex.Model = strings.TrimSpace(task.Model)
	}
	if strings.TrimSpace(task.ReasoningEffort) != "" {
		timerCodex.ReasoningEffort = strings.TrimSpace(task.ReasoningEffort)
	}
	fmt.Fprintln(w, "TIMER_TASK_START")
	fmt.Fprintf(w, "timer_task_id=%s\n", task.ID)
	fmt.Fprintf(w, "timer_task_file=%s\n", taskFile)
	fmt.Fprintf(w, "timer_task_chat_id=%s\n", task.ChatID)
	fmt.Fprintf(w, "timer_task_cwd=%s\n", emptyFallback(timerCodex.Cwd, optsFallbackCwd()))
	progress := startProgressReporter(ctx, client, task.ChatID, cfg, task.Prompt)

	codexReply, err := runCodexFunc(ctx, timerCodex, CodexRunRequest{
		Prompt: buildPrompt(cfg, task.ChatID, task.Prompt, "定时任务 | "+task.ID, nil),
		OnEvent: func(event codexProgressEvent) {
			if progress != nil {
				progress.recordEvent(ctx, event)
			}
		},
	})
	if err != nil {
		fmt.Fprintf(w, "TIMER_TASK_ERROR id=%s message=%s\n", task.ID, compactText(err.Error(), 400))
		if progress != nil {
			progress.fail(ctx, "定时任务执行失败："+err.Error())
		}
		_ = sendTextReply(ctx, client, task.ChatID, "定时任务 "+task.ID+" 执行失败："+err.Error())
		return err
	}
	reply, err := sendReplyWithAttachments(ctx, client, task.ChatID, codexReply.Reply, emptyFallback(timerCodex.Cwd, optsFallbackCwd()), cfg)
	if err != nil {
		fmt.Fprintf(w, "TIMER_TASK_ERROR id=%s message=%s\n", task.ID, compactText(err.Error(), 400))
		if progress != nil {
			progress.fail(ctx, "定时任务执行失败："+err.Error())
		}
		return err
	}
	finishProgressReporter(ctx, progress, "定时任务执行完成，结果见下条消息。", reply)
	fmt.Fprintf(w, "TIMER_TASK_OK id=%s\n", task.ID)
	return nil
}

func buildCodexExecutionFailureReply(err error) string {
	details := ""
	if err != nil {
		details = compactText(strings.TrimSpace(err.Error()), 1200)
	}
	lines := []string{"处理失败：Codex 执行失败。"}
	lower := strings.ToLower(details)
	if strings.Contains(lower, "responses_websocket") || strings.Contains(lower, "/v1/responses") {
		lines = append(lines, "请检查 codex.base_url / OPENAI_BASE_URL 指向的服务是否支持 Responses websocket。")
	}
	if details != "" {
		lines = append(lines, "详情："+details)
	}
	return strings.Join(lines, "\n")
}

func finishProgressReporter(ctx context.Context, progress progressReporter, note, finalReply string) {
	if progress == nil {
		return
	}
	progress.complete(ctx, note)
	progress.recordFinalReply(ctx, finalReply)
}

func sendReplyWithAttachments(ctx context.Context, client *lark.Client, chatID, rawReply, cwd string, cfg Config) (string, error) {
	attachmentPlan := extractAttachmentDirectives(strings.ReplaceAll(rawReply, "\r", ""))
	reply := attachmentPlan.Text
	if strings.TrimSpace(reply) == "" && len(attachmentPlan.Attachments) == 0 {
		reply = "暂时没有生成可发送的回复。"
	}
	if strings.TrimSpace(reply) != "" {
		if err := sendCodexReplyPassthrough(ctx, client, chatID, reply, cfg); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return reply, err
	}
	attachmentResult := sendRequestedAttachments(ctx, client, chatID, attachmentPlan.Attachments, cwd)
	if err := ctx.Err(); err != nil {
		return reply, err
	}
	if strings.TrimSpace(reply) == "" && len(attachmentResult.Sent) > 0 {
		if defaultReply := buildDefaultAttachmentReply(attachmentResult.Sent); defaultReply != "" {
			if err := sendTextReply(ctx, client, chatID, defaultReply); err != nil {
				return "", err
			}
			reply = defaultReply
		}
	}
	if failureReply := buildAttachmentFailureReply(attachmentResult.Sent, attachmentResult.Failed); failureReply != "" {
		if err := ctx.Err(); err != nil {
			return reply, err
		}
		if err := sendTextReply(ctx, client, chatID, failureReply); err != nil {
			return "", err
		}
		if strings.TrimSpace(reply) == "" {
			reply = failureReply
		}
	}
	finalReplyForLog := strings.TrimSpace(strings.Join([]string{
		strings.TrimSpace(reply),
		buildAttachmentSendResultText(attachmentResult.Sent, attachmentResult.Failed),
	}, "\n"))
	if finalReplyForLog != "" {
		return finalReplyForLog, nil
	}
	return reply, nil
}

func buildIncomingImagePrompt(ctx context.Context, client *lark.Client, messageID string, imageKeys []string, text string) (string, string, []string, []string, error) {
	imagePaths := []string{}
	tempCleanup := []string{}
	for _, imageKey := range imageKeys {
		tempDir, filePath, err := downloadImageToTempFile(ctx, client, messageID, imageKey)
		if err != nil {
			for _, path := range tempCleanup {
				_ = os.RemoveAll(path)
			}
			return "", "", nil, nil, err
		}
		tempCleanup = append(tempCleanup, tempDir)
		imagePaths = append(imagePaths, filePath)
	}
	if strings.TrimSpace(text) != "" {
		return strings.Join([]string{
				fmt.Sprintf("用户发送了 %d 张图片，并附带文字：", len(imagePaths)),
				strings.TrimSpace(text),
				"本地临时图片路径：",
				strings.Join(imagePaths, "\n"),
				"请结合文字和图片内容回答。",
			}, "\n"),
			fmt.Sprintf("[图片消息 %d 张 + 文本] %s", len(imagePaths), strings.TrimSpace(text)),
			append([]string(nil), imagePaths...),
			tempCleanup,
			nil
	}
	return strings.Join([]string{
			fmt.Sprintf("用户发送了 %d 张图片，请直接分析图片内容并给出有帮助的回复。", len(imagePaths)),
			"本地临时图片路径：",
			strings.Join(imagePaths, "\n"),
		}, "\n"),
		fmt.Sprintf("[图片消息] 用户发送了 %d 张图片", len(imagePaths)),
		append([]string(nil), imagePaths...),
		tempCleanup,
		nil
}

func parseIncomingText(messageType, rawContent string) (string, string) {
	switch strings.TrimSpace(strings.ToLower(messageType)) {
	case "text":
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(rawContent), &payload); err == nil {
			return strings.TrimSpace(payload.Text), ""
		}
	case "post":
		parsed := parsePostMessageContent(rawContent)
		if parsed.Text != "" {
			return parsed.Text, ""
		}
	}
	return "", ""
}

func shouldReply(cfg Config, message *larkim.EventMessage, chatType string, text string, textualMention string, allowMentionCarry bool) bool {
	if !cfg.RequireMention {
		return true
	}
	if strings.TrimSpace(chatType) == "p2p" && cfg.RequireMentionGroupOnly {
		return true
	}
	if mentionedByEvent(cfg, message) {
		return true
	}
	if strings.TrimSpace(textualMention) != "" || detectTextualBotMention(text, cfg.MentionAliases) != "" {
		return true
	}
	if allowMentionCarry && isGroupChat(chatType) {
		return true
	}
	return false
}

func isCancelledError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func cancelReason(task *chatTaskControl, err error) string {
	if task != nil && strings.TrimSpace(task.CancelReason()) != "" {
		return task.CancelReason()
	}
	if err != nil {
		return compactText(err.Error(), 200)
	}
	return "cancelled"
}

func mentionedByEvent(cfg Config, message *larkim.EventMessage) bool {
	if message == nil || len(message.Mentions) == 0 {
		return false
	}
	for _, mention := range message.Mentions {
		if mention == nil {
			continue
		}
		id := ""
		if mention.Id != nil {
			id = strings.TrimSpace(deref(mention.Id.OpenId))
		}
		name := normalizeAlias(deref(mention.Name))
		effective := getEffectiveBotOpenID(cfg)
		if id != "" && effective.Value != "" && id == effective.Value {
			return true
		}
		for _, alias := range cfg.MentionAliases {
			if alias != "" && strings.EqualFold(name, normalizeAlias(alias)) {
				return true
			}
		}
	}
	return false
}

func stripLeadingMentions(text string, aliases []string) string {
	return strings.TrimSpace(stripLeadingTextMentions(text, aliases))
}

func chunkText(text string, max int) []string {
	if max <= 0 || len([]rune(text)) <= max {
		return []string{text}
	}
	runes := []rune(text)
	out := []string{}
	for len(runes) > 0 {
		n := max
		if len(runes) < n {
			n = len(runes)
		}
		out = append(out, strings.TrimSpace(string(runes[:n])))
		runes = runes[n:]
	}
	return out
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func ternary(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

func deref[T any](ptr *T) T {
	var zero T
	if ptr == nil {
		return zero
	}
	return *ptr
}

func optsFallbackCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func buildCodexThreadTitle(cfg Config, localThreadName, userText string) string {
	botLabel := compactText(strings.Join(strings.Fields(emptyFallback(cfg.BotName, cfg.AccountName)), " "), 24)
	threadLabel := compactText(strings.Join(strings.Fields(emptyFallback(localThreadName, "主线程")), " "), 18)
	userLabel := compactText(strings.Join(strings.Fields(strings.TrimSpace(userText)), " "), 42)
	parts := []string{}
	for _, item := range []string{botLabel, threadLabel, userLabel} {
		if strings.TrimSpace(item) != "" {
			parts = append(parts, item)
		}
	}
	return strings.Join(parts, " | ")
}
