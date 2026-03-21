package feishunative

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkdocx "github.com/larksuite/oapi-sdk-go/v3/service/docx/v1"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const (
	feishuDocxTextBlockType     = 2
	feishuDocxHeading2BlockType = 4
	feishuDocxHeading3BlockType = 5
	feishuDocxCodeBlockType     = 14
	feishuTextChunkLimit        = 4000
	feishuMarkdownChunkLimit    = 4000
)

type typingIndicatorState struct {
	MessageID  string
	ReactionID string
}

type progressReporter interface {
	abort(ctx context.Context, note string)
	complete(ctx context.Context, note string)
	fail(ctx context.Context, note string)
	recordFinalReply(ctx context.Context, finalReply string)
	recordEvent(ctx context.Context, event codexProgressEvent)
}

type silentProgressReporter struct{}

type messageProgressReporter struct {
	client    *lark.Client
	chatID    string
	messageID string
	intro     string
	startedAt time.Time
	steps     []string
	lastPush  time.Time
}

type docProgressReporter struct {
	client            *lark.Client
	chatID            string
	cfg               Config
	documentID        string
	documentURL       string
	statusBlockID     string
	linkMessageID     string
	fallback          progressReporter
	finalReplyWritten bool
	startedAt         time.Time
	lastStatusText    string
	lastFlushAt       time.Time
	pendingBlocks     []*larkdocx.Block
	stepHistory       []string
	flushTimer        *time.Timer
	closed            bool
	mu                sync.Mutex
}

const docProgressMinUpdateInterval = 1200 * time.Millisecond

func sendTextReply(ctx context.Context, client *lark.Client, chatID, text string) error {
	_, err := sendTextReplyReturningIDs(ctx, client, chatID, text)
	return err
}

func createMessage(ctx context.Context, client *lark.Client, chatID, msgType, content string) (string, error) {
	body := larkim.NewCreateMessageReqBodyBuilder().
		ReceiveId(chatID).
		MsgType(msgType).
		Content(content).
		Build()
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(body).
		Build()
	resp, err := client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return "", err
	}
	if resp == nil || !resp.Success() {
		return "", fmt.Errorf("send message failed")
	}
	if resp.Data == nil {
		return "", nil
	}
	return strings.TrimSpace(deref(resp.Data.MessageId)), nil
}

func sendTextReplyReturningIDs(ctx context.Context, client *lark.Client, chatID, text string) ([]string, error) {
	chunks := chunkText(strings.TrimSpace(text), 1800)
	messageIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		messageID, err := createTextMessage(ctx, client, chatID, chunk)
		if err != nil {
			return messageIDs, err
		}
		if messageID != "" {
			messageIDs = append(messageIDs, messageID)
		}
	}
	return messageIDs, nil
}

func createTextMessage(ctx context.Context, client *lark.Client, chatID, text string) (string, error) {
	content, _ := json.Marshal(map[string]string{"text": strings.TrimSpace(text)})
	return createMessage(ctx, client, chatID, larkim.MsgTypeText, string(content))
}

func sendMarkdownCardReply(ctx context.Context, client *lark.Client, chatID, markdown string) error {
	safeMarkdown := strings.TrimSpace(strings.ReplaceAll(markdown, "\r", ""))
	if safeMarkdown == "" {
		return nil
	}
	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
			"enable_forward":   true,
		},
		"elements": []map[string]any{
			{
				"tag":     "markdown",
				"content": safeMarkdown,
			},
		},
	}
	content, _ := json.Marshal(card)
	_, err := createMessage(ctx, client, chatID, larkim.MsgTypeInteractive, string(content))
	return err
}

func updateTextMessage(ctx context.Context, client *lark.Client, messageID, text string) error {
	content, _ := json.Marshal(map[string]string{"text": strings.TrimSpace(text)})
	req := larkim.NewUpdateMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewUpdateMessageReqBodyBuilder().MsgType(larkim.MsgTypeText).Content(string(content)).Build()).
		Build()
	resp, err := client.Im.V1.Message.Update(ctx, req)
	if err != nil {
		return err
	}
	if resp == nil || !resp.Success() {
		return fmt.Errorf("update message failed")
	}
	return nil
}

func recallMessage(ctx context.Context, client *lark.Client, messageID string) error {
	req := larkim.NewDeleteMessageReqBuilder().
		MessageId(strings.TrimSpace(messageID)).
		Build()
	resp, err := client.Im.V1.Message.Delete(ctx, req)
	if err != nil {
		return err
	}
	if resp == nil || !resp.Success() {
		return fmt.Errorf("recall message failed")
	}
	return nil
}

func sendTextReplyWithFakeStream(ctx context.Context, client *lark.Client, chatID, text string, cfg Config) error {
	finalText := strings.TrimSpace(text)
	if finalText == "" {
		return nil
	}
	if !cfg.FakeStreamEnabled || len(chunkText(finalText, 1800)) > 1 {
		return sendTextReply(ctx, client, chatID, finalText)
	}

	steps := buildFakeStreamSteps(finalText, cfg.FakeStreamChunkChars, cfg.FakeStreamMaxUpdates)
	if len(steps) <= 1 {
		return sendTextReply(ctx, client, chatID, finalText)
	}

	messageID, err := createTextMessage(ctx, client, chatID, steps[0])
	if err != nil || messageID == "" {
		return err
	}
	for _, step := range steps[1:] {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(maxInt(cfg.FakeStreamIntervalMS, 10)) * time.Millisecond):
		}
		if err := updateTextMessage(ctx, client, messageID, step); err != nil {
			return err
		}
	}
	return nil
}

func splitTextForFeishu(text string, maxLength int) []string {
	raw := strings.ReplaceAll(text, "\r", "")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	max := maxInt(maxLength, 500)
	chunks := []string{}
	cursor := 0
	runes := []rune(raw)
	for cursor < len(runes) {
		end := minInt(cursor+max, len(runes))
		if end < len(runes) {
			for i := end; i > cursor+maxInt(max*6/10, 1); i-- {
				if runes[i-1] == '\n' {
					end = i
					break
				}
			}
		}
		if end <= cursor {
			end = minInt(cursor+max, len(runes))
		}
		chunks = append(chunks, string(runes[cursor:end]))
		cursor = end
	}
	return chunks
}

func shouldRenderFeishuMarkdown(rawText string) bool {
	text := strings.TrimSpace(strings.ReplaceAll(rawText, "\r", ""))
	if text == "" {
		return false
	}
	if strings.Contains(text, "```") {
		return true
	}
	if regexpMatch("`[^`\n]+`", text) || regexpMatch(`\[[^\]]+\]\([^)]+\)`, text) || hasMarkdownEmphasis(text) {
		return true
	}
	lines := strings.Split(text, "\n")
	structuralHits := 0
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if regexpMatch(`^#{1,6}\s`, line) || regexpMatch(`^>\s+`, line) {
			return true
		}
		if regexpMatch(`^\s*[-*+]\s+`, line) || regexpMatch(`^\s*\d+\.\s+`, line) {
			structuralHits++
		}
		if strings.Contains(line, "|") && i+1 < len(lines) && regexpMatch(`^\s*\|?[\s:-]+\|[\s|:-]*$`, strings.TrimSpace(lines[i+1])) {
			return true
		}
	}
	return structuralHits >= 2
}

func sendRenderedReply(ctx context.Context, client *lark.Client, chatID, rawText string, preferMarkdown bool) error {
	normalized := strings.TrimSpace(strings.ReplaceAll(rawText, "\r", ""))
	if normalized == "" {
		return nil
	}
	renderMarkdown := preferMarkdown && shouldRenderFeishuMarkdown(normalized)
	chunkLimit := feishuTextChunkLimit
	if renderMarkdown {
		chunkLimit = feishuMarkdownChunkLimit
	}
	return walkRenderedReplyChunks(ctx, normalized, renderMarkdown, chunkLimit, func(chunk string) error {
		if renderMarkdown {
			if err := sendMarkdownCardReply(ctx, client, chatID, chunk); err == nil {
				return nil
			}
		}
		return sendTextReply(ctx, client, chatID, chunk)
	})
}

func sendCodexReplyPassthrough(ctx context.Context, client *lark.Client, chatID, rawText string, cfg Config) error {
	text := strings.TrimSpace(rawText)
	if text == "" {
		return nil
	}
	if cfg.FakeStreamEnabled && !shouldRenderFeishuMarkdown(text) {
		return sendTextReplyWithFakeStream(ctx, client, chatID, text, cfg)
	}
	return sendRenderedReply(ctx, client, chatID, text, true)
}

func walkRenderedReplyChunks(ctx context.Context, normalized string, renderMarkdown bool, chunkLimit int, send func(chunk string) error) error {
	if strings.TrimSpace(normalized) == "" {
		return nil
	}
	chunks := splitTextForFeishu(normalized, chunkLimit)
	for _, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		if err := send(chunk); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func regexpMatch(pattern, text string) bool {
	return regexp.MustCompile(pattern).MatchString(text)
}

func hasMarkdownEmphasis(text string) bool {
	return regexpMatch(`\*\*[^*\n][\s\S]*?\*\*`, text) ||
		regexpMatch(`__[^_\n][\s\S]*?__`, text) ||
		regexpMatch(`~~[^~\n][\s\S]*?~~`, text)
}

func buildFakeStreamSteps(text string, chunkChars, maxUpdates int) []string {
	value := strings.TrimSpace(text)
	if value == "" {
		return nil
	}
	runes := []rune(value)
	if len(runes) <= 1 {
		return []string{value}
	}
	effectiveStep := maxInt(chunkChars, 1)
	if maxUpdates > 0 {
		effectiveStep = maxInt(effectiveStep, (len(runes)+maxUpdates-1)/maxUpdates)
	}
	steps := []string{}
	for i := effectiveStep; i < len(runes); i += effectiveStep {
		steps = append(steps, strings.TrimSpace(string(runes[:i])))
	}
	steps = append(steps, value)
	return uniqueStrings(steps...)
}

func addTypingIndicatorSafe(ctx context.Context, client *lark.Client, messageID, emoji string) *typingIndicatorState {
	if strings.TrimSpace(messageID) == "" {
		return nil
	}
	resp, err := client.Im.V1.MessageReaction.Create(ctx, larkim.NewCreateMessageReactionReqBuilder().
		MessageId(messageID).
		Body(larkim.NewCreateMessageReactionReqBodyBuilder().
			ReactionType(larkim.NewEmojiBuilder().EmojiType(emptyFallback(emoji, "Typing")).Build()).
			Build()).
		Build())
	if err != nil || resp == nil || !resp.Success() || resp.Data == nil {
		return nil
	}
	return &typingIndicatorState{
		MessageID:  messageID,
		ReactionID: strings.TrimSpace(deref(resp.Data.ReactionId)),
	}
}

func removeTypingIndicatorSafe(ctx context.Context, client *lark.Client, state *typingIndicatorState) {
	if state == nil || strings.TrimSpace(state.MessageID) == "" || strings.TrimSpace(state.ReactionID) == "" {
		return
	}
	_, _ = client.Im.V1.MessageReaction.Delete(ctx, larkim.NewDeleteMessageReactionReqBuilder().
		MessageId(state.MessageID).
		ReactionId(state.ReactionID).
		Build())
}

func startProgressReporter(ctx context.Context, client *lark.Client, chatID string, cfg Config, userText string) progressReporter {
	if !cfg.Progress.Enabled || strings.TrimSpace(chatID) == "" {
		return nil
	}
	if strings.TrimSpace(cfg.Progress.Mode) == "doc" {
		reporter := newDocProgressReporter(ctx, client, chatID, cfg, userText)
		if reporter != nil {
			return reporter
		}
		fmt.Println("progress_doc_fallback=silent")
	}
	return newMessageProgressReporter(ctx, client, chatID, cfg)
}

func newMessageProgressReporter(ctx context.Context, client *lark.Client, chatID string, cfg Config) *messageProgressReporter {
	intro := emptyFallback(cfg.Progress.Message, "已接收，正在执行。")
	messageID, err := createTextMessage(ctx, client, chatID, intro)
	if err != nil {
		return nil
	}
	return &messageProgressReporter{
		client:    client,
		chatID:    chatID,
		messageID: messageID,
		intro:     intro,
		startedAt: time.Now(),
		lastPush:  time.Now(),
	}
}

func (p *messageProgressReporter) complete(ctx context.Context, note string) {
	if p == nil {
		return
	}
	if strings.TrimSpace(p.messageID) == "" {
		if strings.TrimSpace(note) != "" {
			_ = sendTextReply(ctx, p.client, p.chatID, note)
		}
		return
	}
	if strings.TrimSpace(note) == "" {
		note = "执行完成，回复见下条消息。"
	}
	_ = updateTextMessage(ctx, p.client, p.messageID, note)
}

func (p *messageProgressReporter) abort(ctx context.Context, note string) {
	if p == nil {
		return
	}
	if strings.TrimSpace(p.messageID) == "" {
		if strings.TrimSpace(note) != "" {
			_ = sendTextReply(ctx, p.client, p.chatID, note)
		}
		return
	}
	if err := recallMessage(ctx, p.client, p.messageID); err != nil && strings.TrimSpace(note) != "" {
		_ = updateTextMessage(ctx, p.client, p.messageID, note)
	}
	p.messageID = ""
}

func (p *messageProgressReporter) fail(ctx context.Context, note string) {
	if p == nil {
		return
	}
	if strings.TrimSpace(note) == "" {
		note = "处理失败。"
	}
	if strings.TrimSpace(p.messageID) == "" {
		_ = sendTextReply(ctx, p.client, p.chatID, note)
		return
	}
	_ = updateTextMessage(ctx, p.client, p.messageID, note)
}

func (p *messageProgressReporter) recordFinalReply(context.Context, string) {}

func newSilentProgressReporter() progressReporter {
	return silentProgressReporter{}
}

func (silentProgressReporter) complete(context.Context, string)         {}
func (silentProgressReporter) abort(context.Context, string)            {}
func (silentProgressReporter) fail(context.Context, string)             {}
func (silentProgressReporter) recordFinalReply(context.Context, string) {}
func (silentProgressReporter) recordEvent(context.Context, codexProgressEvent) {
}

func (p *messageProgressReporter) pushStep(ctx context.Context, step string) {
	if p == nil {
		return
	}
	step = strings.TrimSpace(step)
	if step == "" {
		return
	}
	if len(p.steps) > 0 && p.steps[len(p.steps)-1] == step {
		return
	}
	p.steps = append(p.steps, step)
	if len(p.steps) > 10 {
		p.steps = p.steps[len(p.steps)-10:]
	}
	if strings.TrimSpace(p.messageID) == "" {
		return
	}
	lines := []string{
		p.intro,
		"",
		fmt.Sprintf("运行中：%ds", int(time.Since(p.startedAt).Seconds())),
	}
	if len(p.steps) > 0 {
		lines = append(lines, "当前步骤："+p.steps[len(p.steps)-1], "", "步骤记录：")
		for idx, item := range p.steps {
			lines = append(lines, fmt.Sprintf("%d. %s", idx+1, item))
		}
	}
	_ = updateTextMessage(ctx, p.client, p.messageID, compactText(strings.Join(lines, "\n"), 4000))
	p.lastPush = time.Now()
}

func (p *messageProgressReporter) recordEvent(ctx context.Context, event codexProgressEvent) {
	if p == nil {
		return
	}
	step := formatCodexProgressEvent(event)
	if strings.TrimSpace(step) == "" {
		return
	}
	if time.Since(p.lastPush) < 700*time.Millisecond {
		return
	}
	p.pushStep(ctx, step)
}

func newDocProgressReporter(ctx context.Context, client *lark.Client, chatID string, cfg Config, userText string) *docProgressReporter {
	reporter := &docProgressReporter{
		client:    client,
		chatID:    chatID,
		cfg:       cfg,
		startedAt: time.Now(),
	}
	if err := reporter.initialize(ctx, userText); err != nil {
		fmt.Printf("progress_doc_init=error chat_id=%s message=%s\n", chatID, err.Error())
		reporter.fallback = newSilentProgressReporter()
	}
	return reporter
}

func (p *docProgressReporter) initialize(ctx context.Context, userText string) error {
	title := fmt.Sprintf("%s %s", emptyFallback(p.cfg.Progress.Doc.TitlePrefix, "AI 助手｜任务进度"), formatProgressTimestamp(p.startedAt))
	resp, err := p.client.Docx.V1.Document.Create(ctx, larkdocx.NewCreateDocumentReqBuilder().
		Body(larkdocx.NewCreateDocumentReqBodyBuilder().Title(title).Build()).
		Build())
	if err != nil {
		return err
	}
	if resp == nil || !resp.Success() || resp.Data == nil || resp.Data.Document == nil {
		return fmt.Errorf("progress doc create failed")
	}
	p.documentID = strings.TrimSpace(deref(resp.Data.Document.DocumentId))
	if p.documentID == "" {
		return fmt.Errorf("progress doc create returned empty document_id")
	}

	statusText := p.renderStatus("运行中", emptyFallback(p.cfg.Progress.Message, "已接收，正在执行。"))
	blocks := []*larkdocx.Block{
		buildDocTextBlock(statusText),
	}
	if block := buildDocHeadingBlock(2, "任务概览"); block != nil {
		blocks = append(blocks, block)
	}
	if block := buildDocKeyValueBlock("开始时间", formatProgressTimestamp(p.startedAt), false); block != nil {
		blocks = append(blocks, block)
	}
	if p.cfg.Progress.Doc.IncludeUserMessage {
		if block := buildDocHeadingBlock(2, "用户消息"); block != nil {
			blocks = append(blocks, block)
		}
		blocks = append(blocks, buildDocTextBlocks(compactText(userText, 3000))...)
	}
	if block := buildDocHeadingBlock(2, "进度日志"); block != nil {
		blocks = append(blocks, block)
	}
	blocks = append(blocks, buildDocTextBlocks(emptyFallback(p.cfg.Progress.Message, "已接收，正在执行。"))...)

	created, err := p.client.Docx.V1.DocumentBlockChildren.Create(ctx, larkdocx.NewCreateDocumentBlockChildrenReqBuilder().
		DocumentId(p.documentID).
		BlockId(p.documentID).
		DocumentRevisionId(-1).
		Body(larkdocx.NewCreateDocumentBlockChildrenReqBodyBuilder().Children(blocks).Build()).
		Build())
	if err != nil {
		return err
	}
	if created == nil || !created.Success() || created.Data == nil {
		return fmt.Errorf("progress doc block create failed")
	}
	if len(created.Data.Children) > 0 {
		p.statusBlockID = strings.TrimSpace(deref(created.Data.Children[0].BlockId))
	}
	p.lastStatusText = statusText
	p.lastFlushAt = time.Now()

	if p.cfg.Progress.Doc.ShareToChat {
		if err := p.shareWithChat(ctx); err != nil {
			fmt.Printf("progress_doc_share_chat=error document_id=%s chat_id=%s message=%s\n", p.documentID, p.chatID, err.Error())
		}
	}
	if err := p.patchLinkScope(ctx); err != nil {
		fmt.Printf("progress_doc_share_link=error document_id=%s scope=%s message=%s\n", p.documentID, p.cfg.Progress.Doc.LinkScope, err.Error())
	}
	p.documentURL = p.queryURL(ctx)
	linkText := ""
	if p.documentURL != "" {
		linkText = "进度文档：" + p.documentURL + "\n后续过程会持续写入该文档。"
	} else {
		linkText = "进度文档已创建，文档 ID：" + p.documentID + "\n后续过程会持续写入该文档。"
	}
	messageID, err := createTextMessage(ctx, p.client, p.chatID, linkText)
	if err == nil {
		p.linkMessageID = messageID
	}
	return nil
}

func (p *docProgressReporter) complete(ctx context.Context, note string) {
	if p == nil {
		return
	}
	if p.fallback != nil {
		p.fallback.complete(ctx, note)
		p.recallLinkMessage(context.Background(), "complete")
		return
	}
	if strings.TrimSpace(note) == "" {
		note = "执行完成，回复见下条消息。"
	}
	p.closeAndFlush(ctx, "已完成", note)
	p.recallLinkMessage(context.Background(), "complete")
}

func (p *docProgressReporter) abort(ctx context.Context, note string) {
	if p == nil {
		return
	}
	if p.fallback != nil {
		p.fallback.abort(ctx, note)
		p.recallLinkMessage(context.Background(), "abort")
		return
	}
	if strings.TrimSpace(note) == "" {
		note = "当前任务已取消。"
	}
	p.closeAndFlush(ctx, "已取消", note)
	p.recallLinkMessage(context.Background(), "abort")
}

func (p *docProgressReporter) fail(ctx context.Context, note string) {
	if p == nil {
		return
	}
	if p.fallback != nil {
		p.fallback.fail(ctx, note)
		p.recallLinkMessage(context.Background(), "fail")
		return
	}
	if strings.TrimSpace(note) == "" {
		note = "处理失败。"
	}
	p.closeAndFlush(ctx, "失败", note)
	p.recallLinkMessage(context.Background(), "fail")
}

func (p *docProgressReporter) recordFinalReply(ctx context.Context, finalReply string) {
	if p == nil {
		return
	}
	if p.fallback != nil {
		p.fallback.recordFinalReply(ctx, finalReply)
		return
	}
	if !p.cfg.Progress.Doc.WriteFinalReply {
		return
	}
	reply := strings.TrimSpace(finalReply)
	if reply == "" {
		return
	}
	blocks := []*larkdocx.Block{}
	if block := buildDocHeadingBlock(2, "最终回复"); block != nil {
		blocks = append(blocks, block)
	}
	blocks = append(blocks, buildDocTextBlocks(reply)...)
	if err := p.appendBlocks(ctx, blocks); err == nil {
		p.finalReplyWritten = true
	}
}

func (p *docProgressReporter) recordEvent(ctx context.Context, event codexProgressEvent) {
	if p == nil {
		return
	}
	if p.fallback != nil {
		p.fallback.recordEvent(ctx, event)
		return
	}
	formatted := formatCodexProgressEventForDoc(event)
	if formatted.Summary == "" && len(formatted.Sections) == 0 && strings.TrimSpace(formatted.Title) == "" {
		return
	}
	blocks := []*larkdocx.Block{}
	if formatted.Kind == "detail" {
		if block := buildDocHeadingBlock(3, fmt.Sprintf("[%s] %s", formatProgressTimestamp(time.Now()), formatted.Title)); block != nil {
			blocks = append(blocks, block)
		}
		for _, item := range formatted.Meta {
			if block := buildDocKeyValueBlock(item.Label, item.Value, item.InlineCode); block != nil {
				blocks = append(blocks, block)
			}
		}
		for _, section := range formatted.Sections {
			if strings.TrimSpace(section.Label) != "" {
				if block := buildDocLabelBlock(section.Label); block != nil {
					blocks = append(blocks, block)
				}
			}
			if section.Format == "code" {
				blocks = append(blocks, buildDocCodeBlocks(section.Content)...)
			} else {
				blocks = append(blocks, buildDocTextBlocks(section.Content)...)
			}
		}
	} else {
		line := fmt.Sprintf("[%s] %s", formatProgressTimestamp(time.Now()), formatted.Summary)
		blocks = append(blocks, buildDocTextBlock(line))
	}
	if len(blocks) == 0 {
		return
	}
	p.queueBlocks(ctx, blocks, "运行中")
}

func (p *docProgressReporter) appendLogLines(ctx context.Context, lines ...string) {
	blocks := []*larkdocx.Block{}
	for _, line := range lines {
		for _, block := range buildDocTextBlocks(line) {
			blocks = append(blocks, block)
		}
	}
	if len(blocks) == 0 {
		return
	}
	p.queueBlocks(ctx, blocks, "运行中")
}

func (p *docProgressReporter) appendBlocks(ctx context.Context, blocks []*larkdocx.Block) error {
	children := make([]*larkdocx.Block, 0, len(blocks))
	for _, block := range blocks {
		if block != nil {
			children = append(children, block)
		}
	}
	if len(children) == 0 {
		return nil
	}
	resp, err := p.client.Docx.V1.DocumentBlockChildren.Create(ctx, larkdocx.NewCreateDocumentBlockChildrenReqBuilder().
		DocumentId(p.documentID).
		BlockId(p.documentID).
		DocumentRevisionId(-1).
		Body(larkdocx.NewCreateDocumentBlockChildrenReqBodyBuilder().Children(children).Build()).
		Build())
	if err != nil {
		return err
	}
	if resp == nil || !resp.Success() {
		return fmt.Errorf("append doc blocks failed")
	}
	return nil
}

func (p *docProgressReporter) updateStatus(ctx context.Context, text string) {
	if strings.TrimSpace(p.statusBlockID) == "" {
		return
	}
	if strings.TrimSpace(text) == strings.TrimSpace(p.lastStatusText) {
		return
	}
	_, err := p.client.Docx.V1.DocumentBlock.Patch(ctx, larkdocx.NewPatchDocumentBlockReqBuilder().
		DocumentId(p.documentID).
		BlockId(p.statusBlockID).
		DocumentRevisionId(-1).
		UpdateBlockRequest(larkdocx.NewUpdateBlockRequestBuilder().
			UpdateTextElements(larkdocx.NewUpdateTextElementsRequestBuilder().Elements(buildDocTextElements(text, false)).Build()).
			Build()).
		Build())
	if err != nil {
		fmt.Printf("progress_doc_status=error document_id=%s message=%s\n", p.documentID, err.Error())
		_ = p.activateFallback(ctx, "doc_status_failed", err)
		return
	}
	p.lastStatusText = strings.TrimSpace(text)
}

func (p *docProgressReporter) queryURL(ctx context.Context) string {
	resp, err := p.client.Drive.V1.Meta.BatchQuery(ctx, larkdrive.NewBatchQueryMetaReqBuilder().
		MetaRequest(larkdrive.NewMetaRequestBuilder().
			RequestDocs([]*larkdrive.RequestDoc{
				larkdrive.NewRequestDocBuilder().DocToken(p.documentID).DocType("docx").Build(),
			}).
			WithUrl(true).
			Build()).
		Build())
	if err != nil || resp == nil || !resp.Success() || resp.Data == nil || len(resp.Data.Metas) == 0 {
		return ""
	}
	return strings.TrimSpace(deref(resp.Data.Metas[0].Url))
}

func (p *docProgressReporter) shareWithChat(ctx context.Context) error {
	resp, err := p.client.Drive.V1.PermissionMember.BatchCreate(ctx, larkdrive.NewBatchCreatePermissionMemberReqBuilder().
		Token(p.documentID).
		Type("docx").
		NeedNotification(false).
		Body(larkdrive.NewBatchCreatePermissionMemberReqBodyBuilder().
			Members([]*larkdrive.BaseMember{
				larkdrive.NewBaseMemberBuilder().
					MemberType(larkdrive.MemberTypeOpenChat).
					MemberId(p.chatID).
					Perm(larkdrive.PermView).
					Type("chat").
					Build(),
			}).
			Build()).
		Build())
	if err != nil {
		return err
	}
	if resp == nil || !resp.Success() {
		return fmt.Errorf("share doc with chat failed")
	}
	return nil
}

func (p *docProgressReporter) patchLinkScope(ctx context.Context) error {
	scope := strings.ToLower(strings.TrimSpace(p.cfg.Progress.Doc.LinkScope))
	if scope == "" || scope == "closed" {
		return nil
	}
	shareEntity := larkdrive.ShareEntitySameTenant
	linkEntity := larkdrive.LinkShareEntityTenantReadable
	if scope == "anyone" {
		shareEntity = larkdrive.ShareEntityAnyone
		linkEntity = larkdrive.LinkShareEntityAnyoneReadable
	}
	resp, err := p.client.Drive.V1.PermissionPublic.Patch(ctx, larkdrive.NewPatchPermissionPublicReqBuilder().
		Token(p.documentID).
		Type("docx").
		PermissionPublicRequest(larkdrive.NewPermissionPublicRequestBuilder().
			ShareEntity(shareEntity).
			LinkShareEntity(linkEntity).
			Build()).
		Build())
	if err != nil {
		return err
	}
	if resp == nil || !resp.Success() {
		return fmt.Errorf("patch doc link scope failed")
	}
	return nil
}

func (p *docProgressReporter) renderStatus(state, latest string) string {
	elapsedSec := int(time.Since(p.startedAt).Seconds())
	latestText := compactText(strings.TrimSpace(latest), 120)
	if latestText == "" {
		latestText = emptyFallback(p.cfg.Progress.Message, "已接收，正在执行。")
	}
	return compactText(fmt.Sprintf("状态：%s｜耗时：%ds｜最新：%s", state, elapsedSec, latestText), 900)
}

func (p *docProgressReporter) queueBlocks(ctx context.Context, blocks []*larkdocx.Block, state string) {
	if p == nil || len(blocks) == 0 {
		return
	}
	p.trackStep(extractLatestDocBlockText(blocks))
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	for _, block := range blocks {
		if block != nil {
			p.pendingBlocks = append(p.pendingBlocks, block)
		}
	}
	shouldFlushNow := time.Since(p.lastFlushAt) >= docProgressMinUpdateInterval
	if shouldFlushNow {
		p.mu.Unlock()
		p.flushPending(ctx, state)
		return
	}
	if p.flushTimer == nil {
		wait := docProgressMinUpdateInterval - time.Since(p.lastFlushAt)
		if wait < 0 {
			wait = 0
		}
		p.flushTimer = time.AfterFunc(wait, func() {
			p.mu.Lock()
			p.flushTimer = nil
			closed := p.closed
			p.mu.Unlock()
			if closed {
				return
			}
			p.flushPending(context.Background(), "运行中")
		})
	}
	p.mu.Unlock()
}

func (p *docProgressReporter) flushPending(ctx context.Context, state string) {
	if p == nil {
		return
	}
	if p.fallback != nil {
		return
	}
	p.mu.Lock()
	if len(p.pendingBlocks) == 0 {
		p.mu.Unlock()
		if strings.TrimSpace(state) != "" {
			p.updateStatus(ctx, p.renderStatus(state, ""))
		}
		return
	}
	blocks := append([]*larkdocx.Block(nil), p.pendingBlocks...)
	p.pendingBlocks = nil
	p.mu.Unlock()

	latest := extractLatestDocBlockText(blocks)
	if strings.TrimSpace(state) != "" {
		p.updateStatus(ctx, p.renderStatus(state, latest))
		if p.fallback != nil {
			return
		}
	}
	if err := p.appendBlocks(ctx, blocks); err != nil {
		fmt.Printf("progress_doc_append=error document_id=%s message=%s\n", p.documentID, err.Error())
		p.mu.Lock()
		p.pendingBlocks = append(blocks, p.pendingBlocks...)
		p.mu.Unlock()
		_ = p.activateFallback(ctx, "doc_append_failed", err)
		return
	}
	p.mu.Lock()
	p.lastFlushAt = time.Now()
	p.mu.Unlock()
}

func (p *docProgressReporter) stopFlushTimer() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.flushTimer != nil {
		p.flushTimer.Stop()
		p.flushTimer = nil
	}
}

func (p *docProgressReporter) closeAndFlush(ctx context.Context, state, note string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.stopFlushTimer()
	p.flushPending(ctx, state)
	if p.fallback != nil {
		switch state {
		case "已完成":
			p.fallback.complete(ctx, note)
		case "失败":
			p.fallback.fail(ctx, note)
		case "已取消":
			p.fallback.abort(ctx, note)
		}
		return
	}
	if strings.TrimSpace(note) != "" {
		blocks := buildDocTextBlocks(note)
		if len(blocks) > 0 {
			_ = p.appendBlocks(ctx, blocks)
		}
	}
	p.updateStatus(ctx, p.renderStatus(state, note))
}

func (p *docProgressReporter) recallLinkMessage(ctx context.Context, reason string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	messageID := p.linkMessageID
	if messageID == "" {
		p.mu.Unlock()
		return false
	}
	p.linkMessageID = ""
	p.mu.Unlock()
	req := larkim.NewDeleteMessageReqBuilder().MessageId(messageID).Build()
	resp, err := p.client.Im.V1.Message.Delete(ctx, req)
	if err != nil || resp == nil || !resp.Success() {
		fmt.Printf("progress_doc_link_recall=error message_id=%s reason=%s\n", messageID, emptyFallback(reason, "unknown"))
		return false
	}
	fmt.Printf("progress_doc_link_recall=ok message_id=%s reason=%s\n", messageID, emptyFallback(reason, "unknown"))
	return true
}

func (p *docProgressReporter) trackStep(step string) {
	step = strings.TrimSpace(step)
	if step == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.stepHistory) > 0 && p.stepHistory[len(p.stepHistory)-1] == step {
		return
	}
	p.stepHistory = append(p.stepHistory, step)
	if len(p.stepHistory) > 10 {
		p.stepHistory = p.stepHistory[len(p.stepHistory)-10:]
	}
}

func (p *docProgressReporter) activateFallback(ctx context.Context, reason string, err error) error {
	if p == nil {
		return nil
	}
	_ = ctx
	p.mu.Lock()
	if p.fallback != nil {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	reporter := newSilentProgressReporter()

	p.mu.Lock()
	if p.fallback == nil {
		p.fallback = reporter
	}
	p.mu.Unlock()
	fmt.Printf("progress_doc_fallback=enabled kind=silent reason=%s message=%s\n", emptyFallback(reason, "unknown"), emptyFallback(errorText(err), ""))
	return nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return compactText(err.Error(), 300)
}

func extractLatestDocBlockText(blocks []*larkdocx.Block) string {
	for i := len(blocks) - 1; i >= 0; i-- {
		text := extractDocBlockText(blocks[i])
		if strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func extractDocBlockText(block *larkdocx.Block) string {
	if block == nil {
		return ""
	}
	if block.Text != nil {
		return extractDocTextElements(block.Text.Elements)
	}
	if block.Heading2 != nil {
		return extractDocTextElements(block.Heading2.Elements)
	}
	if block.Heading3 != nil {
		return extractDocTextElements(block.Heading3.Elements)
	}
	if block.Code != nil {
		return extractDocTextElements(block.Code.Elements)
	}
	return ""
}

func extractDocTextElements(elements []*larkdocx.TextElement) string {
	parts := make([]string, 0, len(elements))
	for _, element := range elements {
		if element == nil || element.TextRun == nil {
			continue
		}
		value := strings.TrimSpace(deref(element.TextRun.Content))
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " ")
}

func buildDocTextElement(content string, bold bool, inlineCode bool) *larkdocx.TextElement {
	styleBuilder := larkdocx.NewTextElementStyleBuilder()
	hasStyle := false
	if bold {
		styleBuilder.Bold(true)
		hasStyle = true
	}
	if inlineCode {
		styleBuilder.InlineCode(true)
		hasStyle = true
	}
	textRunBuilder := larkdocx.NewTextRunBuilder().Content(strings.TrimSpace(content))
	if hasStyle {
		textRunBuilder.TextElementStyle(styleBuilder.Build())
	}
	return larkdocx.NewTextElementBuilder().TextRun(textRunBuilder.Build()).Build()
}

func buildDocTextElements(text string, inlineCode bool) []*larkdocx.TextElement {
	content := strings.TrimSpace(text)
	if content == "" {
		return nil
	}
	return []*larkdocx.TextElement{buildDocTextElement(content, false, inlineCode)}
}

func buildDocTextBlock(text string) *larkdocx.Block {
	elements := buildDocTextElements(text, false)
	if len(elements) == 0 {
		return nil
	}
	return larkdocx.NewBlockBuilder().
		BlockType(feishuDocxTextBlockType).
		Text(larkdocx.NewTextBuilder().Elements(elements).Build()).
		Build()
}

func buildDocHeadingBlock(level int, text string) *larkdocx.Block {
	content := strings.TrimSpace(text)
	if content == "" {
		return nil
	}
	elements := buildDocTextElements(content, false)
	builder := larkdocx.NewBlockBuilder()
	switch level {
	case 2:
		return builder.BlockType(feishuDocxHeading2BlockType).Heading2(larkdocx.NewTextBuilder().Elements(elements).Build()).Build()
	default:
		return builder.BlockType(feishuDocxHeading3BlockType).Heading3(larkdocx.NewTextBuilder().Elements(elements).Build()).Build()
	}
}

func buildDocKeyValueBlock(label, value string, inlineCode bool) *larkdocx.Block {
	safeLabel := strings.TrimSpace(label)
	safeValue := strings.TrimSpace(value)
	if safeLabel == "" || safeValue == "" {
		return nil
	}
	elements := []*larkdocx.TextElement{
		buildDocTextElement(safeLabel+"：", true, false),
		buildDocTextElement(safeValue, false, inlineCode),
	}
	return larkdocx.NewBlockBuilder().
		BlockType(feishuDocxTextBlockType).
		Text(larkdocx.NewTextBuilder().Elements(elements).Build()).
		Build()
}

func buildDocLabelBlock(label string) *larkdocx.Block {
	safeLabel := strings.TrimSpace(label)
	if safeLabel == "" {
		return nil
	}
	return larkdocx.NewBlockBuilder().
		BlockType(feishuDocxTextBlockType).
		Text(larkdocx.NewTextBuilder().Elements([]*larkdocx.TextElement{
			buildDocTextElement(safeLabel+"：", true, false),
		}).Build()).
		Build()
}

func buildDocTextBlocks(text string) []*larkdocx.Block {
	chunks := chunkText(strings.TrimSpace(text), 900)
	blocks := make([]*larkdocx.Block, 0, len(chunks))
	for _, chunk := range chunks {
		if block := buildDocTextBlock(chunk); block != nil {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func buildDocCodeBlocks(text string) []*larkdocx.Block {
	chunks := splitRawTextChunks(strings.TrimSpace(text), 900)
	blocks := make([]*larkdocx.Block, 0, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		blocks = append(blocks, larkdocx.NewBlockBuilder().
			BlockType(feishuDocxCodeBlockType).
			Code(larkdocx.NewTextBuilder().
				Style(larkdocx.NewTextStyleBuilder().Wrap(true).Build()).
				Elements(buildDocTextElements(chunk, false)).
				Build()).
			Build())
	}
	return blocks
}

func splitRawTextChunks(raw string, maxLength int) []string {
	text := strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	max := maxInt(maxLength, 120)
	runes := []rune(text)
	chunks := []string{}
	cursor := 0
	for cursor < len(runes) {
		end := cursor + max
		if end > len(runes) {
			end = len(runes)
		}
		if end < len(runes) {
			for i := end; i > cursor+max/2; i-- {
				if runes[i-1] == '\n' {
					end = i
					break
				}
			}
		}
		if end <= cursor {
			end = minInt(cursor+max, len(runes))
		}
		chunk := string(runes[cursor:end])
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		cursor = end
	}
	return chunks
}

func formatProgressTimestamp(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

type progressMetaItem struct {
	Label      string
	Value      string
	InlineCode bool
}

type progressSection struct {
	Label   string
	Content string
	Format  string
}

type formattedProgressEvent struct {
	Kind     string
	Title    string
	Summary  string
	Meta     []progressMetaItem
	Sections []progressSection
}

func formatCodexProgressEvent(event codexProgressEvent) string {
	typeName := strings.TrimSpace(fmt.Sprint(event["type"]))
	if typeName == "" {
		return ""
	}
	switch typeName {
	case "thread.started":
		return "会话已创建"
	case "thread.completed":
		return "会话已结束"
	case "turn.started":
		return "开始分析消息"
	case "turn.completed":
		return "分析完成，正在生成回复"
	case "turn.failed":
		return "本轮处理失败"
	case "turn.cancelled":
		return "本轮处理已取消"
	case "error":
		return "执行出现错误，正在重试或收尾"
	case "raw":
		return "收到原始输出"
	}
	if strings.HasPrefix(typeName, "item.") {
		itemType := normalizeProgressSnippet(pickNestedString(event, 40, "item", "type"), 40)
		if itemType == "" {
			itemType = "任务"
		}
		switch typeName {
		case "item.started":
			return "开始步骤：" + itemType
		case "item.completed":
			switch itemType {
			case "reasoning":
				return "完成一步推理"
			case "tool_call":
				return "调用工具处理中"
			case "command_execution":
				return "执行命令处理中"
			case "agent_message":
				return "已生成回复草稿"
			default:
				return "完成步骤：" + itemType
			}
		case "item.failed":
			return "步骤失败：" + itemType
		case "item.cancelled":
			return "步骤取消：" + itemType
		default:
			return "步骤更新：" + itemType
		}
	}
	if strings.HasPrefix(typeName, "agent_message.") {
		return "正在组织回复"
	}
	if strings.HasPrefix(typeName, "tool.") {
		return "工具调用处理中"
	}
	return "处理中：" + normalizeProgressSnippet(typeName, 48)
}

func formatCodexProgressEventForDoc(event codexProgressEvent) formattedProgressEvent {
	if strings.EqualFold(strings.TrimSpace(fmt.Sprint(event["type"])), "raw") {
		text := normalizeProgressDetailText(fmt.Sprint(event["text"]), 12000)
		if text == "" {
			return formattedProgressEvent{}
		}
		return formattedProgressEvent{
			Kind:    "detail",
			Title:   "原始输出",
			Summary: "收到原始输出",
			Sections: []progressSection{
				{Label: "原始输出", Content: text, Format: "code"},
			},
		}
	}
	summary := formatCodexProgressEvent(event)
	commandText := firstNonEmpty(
		pickNestedString(event, 6000, "parsed_cmd", "command"),
		pickNestedString(event, 6000, "cmd"),
		pickNestedString(event, 6000, "command_line"),
		pickNestedString(event, 6000, "shell_command"),
	)
	workingDirectory := firstNonEmpty(
		pickNestedString(event, 1200, "working_directory"),
		pickNestedString(event, 1200, "cwd"),
		pickNestedString(event, 1200, "directory"),
	)
	outputChunk := firstNonEmpty(
		pickNestedString(event, 6000, "chunk"),
		pickNestedString(event, 6000, "output_chunk"),
		pickNestedString(event, 6000, "stdout_chunk"),
		pickNestedString(event, 6000, "stderr_chunk"),
	)
	stdout := pickNestedString(event, 6000, "stdout")
	stderr := pickNestedString(event, 6000, "stderr")
	aggregatedOutput := firstNonEmpty(
		pickNestedString(event, 6000, "aggregated_output"),
		pickNestedString(event, 6000, "output"),
	)
	exitCode := firstNonNilInt(
		pickNestedNumber(event, "exit_code"),
		pickNestedNumber(event, "code"),
	)
	typeName := strings.TrimSpace(fmt.Sprint(event["type"]))
	itemType := pickNestedString(event, 120, "item", "type")

	meta := []progressMetaItem{}
	if typeName != "" || itemType != "" {
		meta = append(meta, progressMetaItem{Label: "事件类型", Value: strings.Trim(strings.Join([]string{typeName, itemType}, " / "), " /")})
	}
	if workingDirectory != "" {
		meta = append(meta, progressMetaItem{Label: "工作目录", Value: workingDirectory, InlineCode: true})
	}
	if exitCode != nil {
		meta = append(meta, progressMetaItem{Label: "退出状态", Value: fmt.Sprintf("%d", *exitCode), InlineCode: true})
	}
	sections := []progressSection{}
	if commandText != "" {
		sections = append(sections, progressSection{Label: "执行命令", Content: commandText, Format: "code"})
	}
	if outputChunk != "" {
		sections = append(sections, progressSection{Label: "输出内容", Content: outputChunk, Format: "code"})
	}
	if stdout != "" {
		sections = append(sections, progressSection{Label: "stdout", Content: stdout, Format: "code"})
	}
	if stderr != "" {
		sections = append(sections, progressSection{Label: "stderr", Content: stderr, Format: "code"})
	}
	if aggregatedOutput != "" && outputChunk == "" && stdout == "" && stderr == "" {
		sections = append(sections, progressSection{Label: "输出汇总", Content: aggregatedOutput, Format: "code"})
	}
	if len(meta) > 0 || len(sections) > 0 {
		title := "进度事件"
		if strings.TrimSpace(summary) != "" {
			title = summary
		}
		return formattedProgressEvent{
			Kind:     "detail",
			Title:    title,
			Summary:  summary,
			Meta:     meta,
			Sections: sections,
		}
	}
	return formattedProgressEvent{
		Kind:    "line",
		Summary: summary,
	}
}

func normalizeProgressSnippet(raw string, maxLength int) string {
	max := maxInt(maxLength, 40)
	text := stripAnsi(raw)
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > max {
		return string(runes[:max]) + "..."
	}
	return text
}

func normalizeProgressDetailText(raw string, maxLength int) string {
	max := maxInt(maxLength, 200)
	text := stripAnsi(raw)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\u00a0", " ")
	text = strings.ReplaceAll(text, "\u200b", "")
	lines := strings.Split(text, "\n")
	trimmed := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed = append(trimmed, strings.TrimRight(line, " \t"))
	}
	for len(trimmed) > 0 && strings.TrimSpace(trimmed[0]) == "" {
		trimmed = trimmed[1:]
	}
	for len(trimmed) > 0 && strings.TrimSpace(trimmed[len(trimmed)-1]) == "" {
		trimmed = trimmed[:len(trimmed)-1]
	}
	text = strings.Join(trimmed, "\n")
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > max {
		return string(runes[:max]) + "\n...(已截断)"
	}
	return text
}

func stripAnsi(text string) string {
	var b strings.Builder
	skip := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if skip {
			if (ch >= '@' && ch <= '~') || ch == 'm' {
				skip = false
			}
			continue
		}
		if ch == 0x1b {
			skip = true
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func pickNestedString(root any, maxLength int, path ...string) string {
	value := collectNestedValue(root, path...)
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return normalizeProgressDetailText(typed, maxLength)
	case fmt.Stringer:
		return normalizeProgressDetailText(typed.String(), maxLength)
	default:
		return normalizeProgressDetailText(fmt.Sprint(typed), maxLength)
	}
}

func pickNestedNumber(root any, path ...string) *int {
	value := collectNestedValue(root, path...)
	switch typed := value.(type) {
	case int:
		return &typed
	case int64:
		v := int(typed)
		return &v
	case float64:
		v := int(typed)
		return &v
	case string:
		var v int
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &v); err == nil {
			return &v
		}
	}
	return nil
}

func firstNonNilInt(values ...*int) *int {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func collectNestedValue(root any, path ...string) any {
	current := root
	for _, key := range path {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[key]
			if !ok {
				return nil
			}
			current = next
		case codexProgressEvent:
			next, ok := typed[key]
			if !ok {
				return nil
			}
			current = next
		default:
			return nil
		}
	}
	return current
}

func maxInt(values ...int) int {
	best := 0
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
