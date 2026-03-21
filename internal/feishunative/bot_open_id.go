package feishunative

import (
	"fmt"
	"strings"
	"sync"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"suncodexclaw/internal/configstore"
)

type botOpenIDCandidate struct {
	OpenID string
	Name   string
}

type runtimeBotOpenIDStore struct {
	mu     sync.Mutex
	values map[string]ResolvedValue
}

var globalRuntimeBotOpenIDs = &runtimeBotOpenIDStore{values: map[string]ResolvedValue{}}

func getEffectiveBotOpenID(cfg Config) ResolvedValue {
	account := strings.TrimSpace(cfg.AccountName)
	if account == "" {
		return cfg.BotOpenID
	}
	globalRuntimeBotOpenIDs.mu.Lock()
	defer globalRuntimeBotOpenIDs.mu.Unlock()
	if value, ok := globalRuntimeBotOpenIDs.values[account]; ok && strings.TrimSpace(value.Value) != "" {
		return value
	}
	return cfg.BotOpenID
}

func setRuntimeBotOpenID(account string, value ResolvedValue) {
	account = strings.TrimSpace(account)
	if account == "" || strings.TrimSpace(value.Value) == "" {
		return
	}
	globalRuntimeBotOpenIDs.mu.Lock()
	defer globalRuntimeBotOpenIDs.mu.Unlock()
	globalRuntimeBotOpenIDs.values[account] = value
}

func extractMentionOpenID(mention *larkim.MentionEvent) string {
	if mention == nil {
		return ""
	}
	if mention.Id == nil {
		return ""
	}
	return strings.TrimSpace(deref(mention.Id.OpenId))
}

func extractMentionName(mention *larkim.MentionEvent) string {
	if mention == nil {
		return ""
	}
	return normalizeAlias(deref(mention.Name))
}

func detectBotOpenIDCandidate(mentions []*larkim.MentionEvent, mentionAliases []string) *botOpenIDCandidate {
	aliasSet := map[string]bool{}
	for _, alias := range mentionAliases {
		normalized := normalizeAlias(alias)
		if normalized != "" {
			aliasSet[normalized] = true
		}
	}
	if len(aliasSet) == 0 {
		return nil
	}

	matches := map[string]*botOpenIDCandidate{}
	for _, mention := range mentions {
		openID := extractMentionOpenID(mention)
		name := extractMentionName(mention)
		if openID == "" || name == "" || !aliasSet[name] {
			continue
		}
		matches[openID] = &botOpenIDCandidate{OpenID: openID, Name: name}
	}
	if len(matches) != 1 {
		return nil
	}
	for _, candidate := range matches {
		return candidate
	}
	return nil
}

func reconcileBotOpenIDFromMentions(cfg Config, mentions []*larkim.MentionEvent) *botOpenIDCandidate {
	candidate := detectBotOpenIDCandidate(mentions, cfg.MentionAliases)
	if candidate == nil {
		return nil
	}
	current := getEffectiveBotOpenID(cfg)
	needsPersist := strings.TrimSpace(current.Value) != candidate.OpenID
	action := "auto_detected"
	if strings.TrimSpace(current.Value) != "" {
		action = "reconciled"
	}

	next := ResolvedValue{Value: candidate.OpenID}
	if needsPersist {
		next.Source = "runtime:" + action
	} else {
		next.Source = emptyFallback(current.Source, "config")
	}
	setRuntimeBotOpenID(cfg.AccountName, next)
	if !needsPersist {
		return candidate
	}

	store := configstore.NewStore(cfg.RepoRoot)
	if err := store.WriteOverlay(cfg.AccountName, map[string]any{"bot_open_id": candidate.OpenID}); err != nil {
		fmt.Printf("bot_open_id_%s=error account=%s message=%s\n", action, emptyFallback(cfg.AccountName, "default"), compactText(err.Error(), 400))
		return candidate
	}
	setRuntimeBotOpenID(cfg.AccountName, ResolvedValue{
		Value:  candidate.OpenID,
		Source: "config:" + action,
	})
	fmt.Printf("bot_open_id_%s=ok account=%s name=%s file=%s\n",
		action,
		emptyFallback(cfg.AccountName, "default"),
		emptyFallback(candidate.Name, "(unknown)"),
		store.OverlayTOMLPath(),
	)
	return candidate
}
