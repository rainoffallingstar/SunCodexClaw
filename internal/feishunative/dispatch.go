package feishunative

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const groupMentionCarryWindow = 2 * time.Minute

type conversationScope struct {
	TaskKey  string
	StateKey string
	Kind     string
}

type recentMentionState struct {
	Timestamp time.Time
	Alias     string
}

type recentMentionStore struct {
	mu     sync.Mutex
	states map[string]recentMentionState
}

type dispatchMeta struct {
	ExplicitBotMention bool
	AllowMentionCarry  bool
	MentionNameAlias   string
	TextMentionAlias   string
	CarryAge           time.Duration
	QueuedCarry        bool
	ReceivedAt         time.Time
}

type messageEnvelope struct {
	Event                     *larkim.P2MessageReceiveV1
	Scope                     conversationScope
	Meta                      dispatchMeta
	ShouldSupersedeActiveTask bool
}

type chatTaskControl struct {
	taskKey string
	ctx     context.Context
	cancel  context.CancelFunc

	mu           sync.Mutex
	cancelled    bool
	cancelReason string
	cancelHooks  []func(string)
}

type dispatchRunner struct {
	active   *chatTaskControl
	pending  []*messageEnvelope
	draining bool
}

type messageDispatcher struct {
	baseCtx context.Context
	handler func(context.Context, *messageEnvelope, *chatTaskControl) error

	mu      sync.Mutex
	runners map[string]*dispatchRunner
}

func newRecentMentionStore() *recentMentionStore {
	return &recentMentionStore{states: map[string]recentMentionState{}}
}

func isGroupChat(chatType string) bool {
	return strings.TrimSpace(strings.ToLower(chatType)) != "p2p"
}

func buildConversationScope(chatID, chatType, senderOpenID, messageID string) conversationScope {
	chat := strings.TrimSpace(chatID)
	if chat == "" {
		return conversationScope{Kind: "missing_chat"}
	}
	if !isGroupChat(chatType) {
		return conversationScope{
			TaskKey:  chat,
			StateKey: chat,
			Kind:     "p2p",
		}
	}
	sender := strings.TrimSpace(senderOpenID)
	fallbackMessage := strings.TrimSpace(messageID)
	senderKey := sender
	if senderKey == "" {
		if fallbackMessage != "" {
			senderKey = "message:" + fallbackMessage
		} else {
			senderKey = "unknown_sender"
		}
	}
	scoped := chat + "::" + senderKey
	kind := "group_sender"
	if sender == "" {
		kind = "group_message_fallback"
	}
	return conversationScope{
		TaskKey:  scoped,
		StateKey: scoped,
		Kind:     kind,
	}
}

func isCarryEligibleMessageType(messageType string) bool {
	switch strings.TrimSpace(strings.ToLower(messageType)) {
	case "file", "image", "post", "audio":
		return true
	default:
		return false
	}
}

func detectTextualBotMention(text string, aliases []string) string {
	text = normalizeAliasText(text)
	if text == "" {
		return ""
	}
	boundary := "[\\s\u00a0,，。.!！?？:：;；()（）\\[\\]{}<>《》]"
	for _, alias := range aliases {
		pattern := buildFlexibleAliasPattern(alias)
		if pattern == "" {
			continue
		}
		re := regexp.MustCompile(`(?:^|` + boundary + `)[@＠]\s*` + pattern + `(?:$|` + boundary + `)`)
		if re.MatchString(text) {
			return strings.TrimSpace(alias)
		}
	}
	return ""
}

func escapeRegExp(raw string) string {
	return regexp.QuoteMeta(raw)
}

func normalizeAliasText(raw string) string {
	replacer := strings.NewReplacer("\u200b", "", "\u200c", "", "\u200d", "", "\ufeff", "", "\u00a0", " ")
	text := replacer.Replace(raw)
	return strings.TrimSpace(strings.Join(strings.Fields(text), " "))
}

func buildFlexibleAliasPattern(alias string) string {
	normalized := normalizeAlias(alias)
	if normalized == "" {
		return ""
	}
	return regexp.MustCompile(`\s+`).ReplaceAllString(escapeRegExp(normalized), `\s+`)
}

func stripLeadingTextMentions(raw string, aliases []string) string {
	text := raw
	if text == "" {
		return ""
	}
	previous := ""
	for text != previous {
		previous = text
		for _, alias := range aliases {
			pattern := buildFlexibleAliasPattern(alias)
			if pattern == "" {
				continue
			}
			re := regexp.MustCompile(`^\s*[@＠]\s*` + pattern + `(?:\s*[:：,，;；、-]\s*|\s+|$)`)
			text = re.ReplaceAllString(text, " ")
		}
		text = regexp.MustCompile(`^\s+`).ReplaceAllString(text, "")
	}
	return text
}

func normalizeIncomingText(raw string, mentions []*larkim.MentionEvent, aliases []string) string {
	keys := make([]string, 0, len(mentions))
	for _, mention := range mentions {
		if mention == nil {
			continue
		}
		key := strings.TrimSpace(deref(mention.Key))
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	return normalizeIncomingTextWithMentionKeys(raw, keys, aliases)
}

func normalizeFetchedMessageText(raw string, mentions []*larkim.Mention, aliases []string) string {
	keys := make([]string, 0, len(mentions))
	for _, mention := range mentions {
		if mention == nil {
			continue
		}
		key := strings.TrimSpace(deref(mention.Key))
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	return normalizeIncomingTextWithMentionKeys(raw, keys, aliases)
}

func normalizeIncomingTextWithMentionKeys(raw string, mentionKeys []string, aliases []string) string {
	text := raw
	if text == "" {
		return ""
	}
	for _, key := range mentionKeys {
		text = strings.ReplaceAll(text, key, " ")
	}
	text = regexp.MustCompile(`(?i)<at\b[^>]*>.*?</at>`).ReplaceAllString(text, " ")
	text = stripLeadingTextMentions(text, aliases)
	text = strings.ReplaceAll(text, "\u00a0", " ")
	text = regexp.MustCompile(`^(?:[@＠]\S+\s*)+`).ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func buildMentionCarryKey(chatID, senderOpenID string) string {
	chat := strings.TrimSpace(chatID)
	sender := strings.TrimSpace(senderOpenID)
	if chat == "" || sender == "" {
		return ""
	}
	return chat + ":" + sender
}

func (s *recentMentionStore) remember(chatID, senderOpenID, alias string, now time.Time) {
	if s == nil {
		return
	}
	key := buildMentionCarryKey(chatID, senderOpenID)
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	s.states[key] = recentMentionState{
		Timestamp: now,
		Alias:     strings.TrimSpace(alias),
	}
}

func (s *recentMentionStore) get(chatID, senderOpenID string, now time.Time) *recentMentionState {
	if s == nil {
		return nil
	}
	key := buildMentionCarryKey(chatID, senderOpenID)
	if key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	state, ok := s.states[key]
	if !ok {
		return nil
	}
	copyState := state
	return &copyState
}

func (s *recentMentionStore) pruneLocked(now time.Time) {
	for key, value := range s.states {
		if value.Timestamp.IsZero() || now.Sub(value.Timestamp) > groupMentionCarryWindow {
			delete(s.states, key)
		}
	}
}

func prepareMessageEnvelope(cfg Config, event *larkim.P2MessageReceiveV1, mentionStore *recentMentionStore) *messageEnvelope {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil
	}
	message := event.Event.Message
	chatID := deref(message.ChatId)
	chatType := deref(message.ChatType)
	messageID := deref(message.MessageId)
	messageType := strings.TrimSpace(strings.ToLower(deref(message.MessageType)))
	senderOpenID := ""
	if event.Event.Sender != nil && event.Event.Sender.SenderId != nil {
		senderOpenID = deref(event.Event.Sender.SenderId.OpenId)
	}
	now := time.Now()
	scope := buildConversationScope(chatID, chatType, senderOpenID, messageID)
	rawContent := deref(message.Content)
	rawText, _ := parseIncomingText(messageType, rawContent)
	if messageType == "post" {
		rawText = parsePostMessageContent(rawContent).Text
	}
	botMentionedByID := false
	if message != nil {
		for _, mention := range message.Mentions {
			if mention == nil || mention.Id == nil {
				continue
			}
			id := strings.TrimSpace(deref(mention.Id.OpenId))
			if effective := getEffectiveBotOpenID(cfg); id != "" && strings.TrimSpace(effective.Value) != "" && id == strings.TrimSpace(effective.Value) {
				botMentionedByID = true
				break
			}
		}
	}
	mentionNameAlias := ""
	if len(message.Mentions) > 0 {
		if candidate := reconcileBotOpenIDFromMentions(cfg, message.Mentions); candidate != nil && !botMentionedByID {
			mentionNameAlias = candidate.Name
		}
	}
	textMentionAlias := detectTextualBotMention(rawText, cfg.MentionAliases)
	explicitBotMention := mentionedByEvent(cfg, message) || textMentionAlias != ""
	allowMentionCarry := false
	carryAge := time.Duration(0)
	queuedCarry := false
	if isGroupChat(chatType) {
		if explicitBotMention && senderOpenID != "" && mentionStore != nil {
			mentionStore.remember(chatID, senderOpenID, textMentionAlias, now)
		} else if senderOpenID != "" && isCarryEligibleMessageType(messageType) && mentionStore != nil {
			if recent := mentionStore.get(chatID, senderOpenID, now); recent != nil {
				allowMentionCarry = true
				if !recent.Timestamp.IsZero() {
					carryAge = now.Sub(recent.Timestamp)
				} else {
					queuedCarry = true
				}
			}
		}
	}
	return &messageEnvelope{
		Event: event,
		Scope: scope,
		Meta: dispatchMeta{
			ExplicitBotMention: explicitBotMention,
			AllowMentionCarry:  allowMentionCarry,
			MentionNameAlias:   strings.TrimSpace(mentionNameAlias),
			TextMentionAlias:   strings.TrimSpace(textMentionAlias),
			CarryAge:           carryAge,
			QueuedCarry:        queuedCarry,
			ReceivedAt:         now,
		},
		ShouldSupersedeActiveTask: !isGroupChat(chatType) || explicitBotMention,
	}
}

func newChatTaskControl(parent context.Context, taskKey string) *chatTaskControl {
	ctx, cancel := context.WithCancel(parent)
	return &chatTaskControl{
		taskKey: taskKey,
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (c *chatTaskControl) Context() context.Context {
	if c == nil {
		return context.Background()
	}
	return c.ctx
}

func (c *chatTaskControl) CancelReason() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancelReason
}

func (c *chatTaskControl) IsCancelled() bool {
	if c == nil {
		return false
	}
	select {
	case <-c.ctx.Done():
		return true
	default:
		return false
	}
}

func (c *chatTaskControl) ThrowIfCancelled() error {
	if c == nil {
		return nil
	}
	select {
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
		return nil
	}
}

func (c *chatTaskControl) OnCancel(hook func(string)) {
	if c == nil || hook == nil {
		return
	}
	c.mu.Lock()
	if c.cancelled {
		reason := c.cancelReason
		c.mu.Unlock()
		hook(reason)
		return
	}
	c.cancelHooks = append(c.cancelHooks, hook)
	c.mu.Unlock()
}

func (c *chatTaskControl) Cancel(reason string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	if c.cancelled {
		c.mu.Unlock()
		return false
	}
	c.cancelled = true
	c.cancelReason = emptyFallback(reason, "cancelled")
	hooks := append([]func(string){}, c.cancelHooks...)
	c.mu.Unlock()

	c.cancel()
	for _, hook := range hooks {
		func(fn func(string)) {
			defer func() {
				if recover() != nil {
					fmt.Printf("task_cancel_hook=error task_key=%s\n", c.taskKey)
				}
			}()
			fn(c.CancelReason())
		}(hook)
	}
	return true
}

func newMessageDispatcher(baseCtx context.Context, handler func(context.Context, *messageEnvelope, *chatTaskControl) error) *messageDispatcher {
	return &messageDispatcher{
		baseCtx: baseCtx,
		handler: handler,
		runners: map[string]*dispatchRunner{},
	}
}

func (d *messageDispatcher) Dispatch(envelope *messageEnvelope) {
	if d == nil || envelope == nil {
		return
	}
	taskKey := emptyFallback(envelope.Scope.TaskKey, "unknown")

	var active *chatTaskControl
	startDrain := false

	d.mu.Lock()
	runner, ok := d.runners[taskKey]
	if !ok {
		runner = &dispatchRunner{}
		d.runners[taskKey] = runner
	}
	if runner.active != nil {
		if envelope.ShouldSupersedeActiveTask {
			runner.pending = []*messageEnvelope{envelope}
			active = runner.active
			fmt.Printf("chat_task_supersede task_key=%s\n", taskKey)
		} else {
			runner.pending = append(runner.pending, envelope)
			fmt.Printf("chat_task_queue task_key=%s\n", taskKey)
		}
		d.mu.Unlock()
		if active != nil {
			active.Cancel("superseded_by_new_message")
		}
		return
	}

	runner.pending = append(runner.pending, envelope)
	if !runner.draining {
		runner.draining = true
		startDrain = true
	}
	d.mu.Unlock()

	if startDrain {
		go d.drain(taskKey)
	}
}

func (d *messageDispatcher) drain(taskKey string) {
	for {
		d.mu.Lock()
		runner := d.runners[taskKey]
		if runner == nil {
			d.mu.Unlock()
			return
		}
		if len(runner.pending) == 0 {
			runner.draining = false
			if runner.active == nil {
				delete(d.runners, taskKey)
			}
			d.mu.Unlock()
			return
		}
		envelope := runner.pending[0]
		runner.pending = runner.pending[1:]
		task := newChatTaskControl(d.baseCtx, taskKey)
		runner.active = task
		d.mu.Unlock()

		err := d.handler(task.Context(), envelope, task)
		if err != nil && !task.IsCancelled() && task.Context().Err() == nil {
			fmt.Printf("chat_task_error task_key=%s message=%s\n", taskKey, compactText(err.Error(), 800))
		}

		d.mu.Lock()
		if current := d.runners[taskKey]; current != nil && current.active == task {
			current.active = nil
			if !current.draining && len(current.pending) == 0 {
				delete(d.runners, taskKey)
			}
		}
		d.mu.Unlock()
	}
}
