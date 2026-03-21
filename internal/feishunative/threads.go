package feishunative

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

type historyEntry struct {
	Role string
	Text string
}

type threadState struct {
	ID            string
	Name          string
	CodexThreadID string
	History       []historyEntry
	CreatedAt     int64
	UpdatedAt     int64
}

type chatState struct {
	mu              sync.Mutex
	Threads         map[string]*threadState
	Order           []string
	CurrentThreadID string
	NextThreadSeq   int
}

type chatStateStore struct {
	mu     sync.Mutex
	states map[string]*chatState
}

var globalChatStates = &chatStateStore{states: map[string]*chatState{}}

type threadCommand struct {
	Action string
	Name   string
	Target string
}

func parseThreadCommand(text string) *threadCommand {
	raw := normalizeCommandText(text)
	if raw == "" {
		return nil
	}
	if strings.EqualFold(raw, "/threads") {
		return &threadCommand{Action: "list"}
	}
	if !strings.HasPrefix(strings.ToLower(raw), "/thread") {
		return nil
	}
	switch {
	case matchCommand(raw, `/thread(?:\s+help)?`):
		return &threadCommand{Action: "help"}
	case matchCommand(raw, `/thread\s+list`):
		return &threadCommand{Action: "list"}
	case matchCommand(raw, `/thread\s+current`):
		return &threadCommand{Action: "current"}
	}
	if name, ok := captureCommand(raw, `/thread\s+new(?:\s+(.+))?`); ok {
		return &threadCommand{Action: "new", Name: strings.TrimSpace(name)}
	}
	if target, ok := captureCommand(raw, `/thread\s+switch\s+(.+)`); ok {
		return &threadCommand{Action: "switch", Target: strings.TrimSpace(target)}
	}
	return &threadCommand{Action: "help"}
}

func isResetCommand(text string) bool {
	x := strings.ToLower(normalizeCommandText(text))
	return x == "/reset" || x == "清空上下文" || x == "重置上下文"
}

func ensureChatState(stateKey string) *chatState {
	return globalChatStates.ensure(stateKey)
}

func (s *chatStateStore) ensure(stateKey string) *chatState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.states[stateKey]; ok {
		return st
	}
	first := makeThread("t1", "主线程")
	st := &chatState{
		Threads:         map[string]*threadState{first.ID: first},
		Order:           []string{first.ID},
		CurrentThreadID: first.ID,
		NextThreadSeq:   2,
	}
	s.states[stateKey] = st
	return st
}

func makeThread(threadID, name string) *threadState {
	now := time.Now().UnixMilli()
	threadName := strings.TrimSpace(name)
	if threadName == "" {
		threadName = "线程 " + threadID
	}
	return &threadState{
		ID:        threadID,
		Name:      threadName,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func getCurrentThread(state *chatState) *threadState {
	if state == nil {
		return nil
	}
	return state.Threads[state.CurrentThreadID]
}

func getThreadTurnCount(thread *threadState) int {
	if thread == nil || len(thread.History) == 0 {
		return 0
	}
	return (len(thread.History) + 1) / 2
}

func resolveThreadIDByTarget(state *chatState, target string) string {
	raw := strings.TrimSpace(target)
	if raw == "" || state == nil {
		return ""
	}
	if _, ok := state.Threads[raw]; ok {
		return raw
	}
	if isDigits(raw) {
		if _, ok := state.Threads["t"+raw]; ok {
			return "t" + raw
		}
	}
	lower := strings.ToLower(raw)
	exact := []string{}
	for _, threadID := range state.Order {
		thread := state.Threads[threadID]
		if thread == nil {
			continue
		}
		if strings.ToLower(thread.Name) == lower {
			exact = append(exact, threadID)
		}
	}
	if len(exact) == 1 {
		return exact[0]
	}
	if len(exact) > 1 {
		return "__ambiguous__"
	}
	fuzzy := []string{}
	for _, threadID := range state.Order {
		thread := state.Threads[threadID]
		if thread == nil {
			continue
		}
		if strings.Contains(strings.ToLower(thread.Name), lower) {
			fuzzy = append(fuzzy, threadID)
		}
	}
	if len(fuzzy) == 1 {
		return fuzzy[0]
	}
	if len(fuzzy) > 1 {
		return "__ambiguous__"
	}
	return ""
}

func handleThreadCommand(state *chatState, command *threadCommand) (bool, string) {
	if command == nil {
		return false, ""
	}
	switch command.Action {
	case "help":
		return true, formatThreadHelp()
	case "list":
		return true, formatThreadList(state)
	case "current":
		current := getCurrentThread(state)
		if current == nil {
			return true, "当前线程不存在，请新建线程。"
		}
		return true, fmt.Sprintf("当前线程：%s · %s · %d 轮", current.ID, current.Name, getThreadTurnCount(current))
	case "new":
		threadID := fmt.Sprintf("t%d", state.NextThreadSeq)
		state.NextThreadSeq++
		thread := makeThread(threadID, command.Name)
		state.Threads[threadID] = thread
		state.Order = append(state.Order, threadID)
		state.CurrentThreadID = threadID
		return true, fmt.Sprintf("已创建并切换到新线程：%s · %s", thread.ID, thread.Name)
	case "switch":
		resolved := resolveThreadIDByTarget(state, command.Target)
		if resolved == "__ambiguous__" {
			return true, "匹配到多个线程，请用更精确的线程 ID 或完整名称。"
		}
		if resolved == "" {
			return true, fmt.Sprintf("未找到线程：%s", command.Target)
		}
		state.CurrentThreadID = resolved
		current := getCurrentThread(state)
		return true, fmt.Sprintf("已切换到线程：%s · %s · %d 轮", current.ID, current.Name, getThreadTurnCount(current))
	default:
		return false, ""
	}
}

func formatThreadHelp() string {
	return strings.Join([]string{
		"线程命令：",
		"/threads",
		"/thread list",
		"/thread current",
		"/thread new [名称]",
		"/thread switch <线程ID或名称>",
		"/reset（清空当前线程上下文）",
	}, "\n")
}

func formatThreadList(state *chatState) string {
	if state == nil {
		return "线程列表："
	}
	lines := []string{"线程列表："}
	for _, threadID := range state.Order {
		thread := state.Threads[threadID]
		if thread == nil {
			continue
		}
		marker := ""
		if threadID == state.CurrentThreadID {
			marker = " (当前)"
		}
		lines = append(lines, fmt.Sprintf("%s%s · %s · %d 轮", threadID, marker, thread.Name, getThreadTurnCount(thread)))
	}
	return strings.Join(lines, "\n")
}

func appendHistory(thread *threadState, role, text string, maxTurns int) {
	if thread == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	thread.History = append(thread.History, historyEntry{Role: role, Text: text})
	if maxTurns > 0 {
		maxEntries := maxTurns * 2
		if len(thread.History) > maxEntries {
			thread.History = append([]historyEntry(nil), thread.History[len(thread.History)-maxEntries:]...)
		}
	}
	thread.UpdatedAt = time.Now().UnixMilli()
}

func resetCurrentThread(state *chatState) (string, bool) {
	current := getCurrentThread(state)
	if current == nil {
		return "", false
	}
	current.History = nil
	current.CodexThreadID = ""
	current.UpdatedAt = time.Now().UnixMilli()
	return fmt.Sprintf("已清空当前线程上下文：%s · %s", current.ID, current.Name), true
}

func clearThreadCodexSession(state *chatState, threadID string) {
	if state == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	thread := state.Threads[strings.TrimSpace(threadID)]
	if thread == nil {
		return
	}
	thread.CodexThreadID = ""
	thread.UpdatedAt = time.Now().UnixMilli()
}

func matchCommand(raw, pattern string) bool {
	return strings.TrimSpace(raw) != "" && mustRegexp("^"+pattern+"$", true).MatchString(raw)
}

func captureCommand(raw, pattern string) (string, bool) {
	match := mustRegexp("^"+pattern+"$", true).FindStringSubmatch(raw)
	if len(match) < 2 {
		return "", false
	}
	return match[1], true
}

func mustRegexp(pattern string, caseInsensitive bool) *regexp.Regexp {
	if caseInsensitive {
		pattern = "(?i)" + pattern
	}
	return regexp.MustCompile(pattern)
}

func isDigits(raw string) bool {
	if raw == "" {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
