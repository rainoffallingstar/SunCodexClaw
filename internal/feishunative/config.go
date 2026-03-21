package feishunative

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"

	"suncodexclaw/internal/configstore"
)

type ResolvedValue struct {
	Value  string
	Source string
}

type CodexConfig struct {
	Bin             string
	Model           string
	ReasoningEffort string
	Profile         string
	Cwd             string
	AddDirs         []string
	HistoryTurns    int
	SystemPrompt    string
	APIKey          string
	APIKeySource    string
	BaseURL         string
	Sandbox         string
	ApprovalPolicy  string
}

type SpeechConfig struct {
	Enabled       bool
	Model         string
	Language      string
	APIKey        string
	APIKeySource  string
	BaseURL       string
	FFmpegBin     string
	FFmpegVersion string
}

type ProgressConfig struct {
	Enabled bool
	Message string
	Mode    string
	Doc     ProgressDocConfig
}

type ProgressDocConfig struct {
	TitlePrefix        string
	ShareToChat        bool
	LinkScope          string
	IncludeUserMessage bool
	WriteFinalReply    bool
}

type Config struct {
	RepoRoot                string
	AccountName             string
	Config                  map[string]any
	ConfigPath              string
	DomainLabel             string
	DomainBaseURL           string
	AppID                   ResolvedValue
	AppSecret               ResolvedValue
	EncryptKey              ResolvedValue
	VerificationToken       ResolvedValue
	BotOpenID               ResolvedValue
	AutoReply               bool
	IgnoreSelf              bool
	BotName                 string
	ReplyPrefix             string
	ReplyMode               string
	RequireMention          bool
	RequireMentionGroupOnly bool
	MentionAliases          []string
	TypingIndicatorEnabled  bool
	TypingEmoji             string
	FakeStreamEnabled       bool
	FakeStreamIntervalMS    int
	FakeStreamChunkChars    int
	FakeStreamMaxUpdates    int
	Progress                ProgressConfig
	Codex                   CodexConfig
	Speech                  SpeechConfig
}

func Load(repoRoot, account string) (Config, error) {
	account = strings.TrimSpace(account)
	if account == "" {
		return Config{}, fmt.Errorf("missing required --account <account>")
	}
	store := configstore.NewStore(repoRoot)
	overlay, err := store.ReadOverlay(account)
	if err != nil {
		return Config{}, err
	}
	defaultSecrets, err := store.ReadSecretsEntry("feishu", "default")
	if err != nil {
		return Config{}, err
	}
	accountSecrets, err := store.ReadSecretsEntry("feishu", account)
	if err != nil {
		return Config{}, err
	}
	if len(overlay) == 0 && len(accountSecrets) == 0 {
		return Config{}, fmt.Errorf("feishu config not found: %s %s or %s %s", store.OverlayTOMLPath(), store.OverlayTableLabel(account), store.LocalTOMLPath(), store.SecretsEntryLabel("feishu", account))
	}
	cfgMap := applyDerivedBotConfig(account, configstore.DeepMerge(defaultSecrets, overlay, accountSecrets))

	domainInput := strings.TrimSpace(
		firstNonEmpty(
			accountEnv(account, "DOMAIN"),
			getString(cfgMap, "domain"),
			os.Getenv("FEISHU_DOMAIN"),
			"feishu",
		),
	)
	domainLabel, domainBaseURL, err := resolveDomain(domainInput)
	if err != nil {
		return Config{}, err
	}

	replyMode := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		accountEnv(account, "REPLY_MODE"),
		getString(cfgMap, "reply_mode"),
		os.Getenv("FEISHU_REPLY_MODE"),
		"codex",
	)))
	if replyMode == "" {
		replyMode = "codex"
	}
	if replyMode != "codex" && replyMode != "echo" {
		return Config{}, fmt.Errorf("invalid reply_mode %q, expected codex | echo", replyMode)
	}

	appID := resolveValue(account, "APP_ID", getString(cfgMap, "app_id"), "FEISHU_APP_ID")
	appSecret := resolveValue(account, "APP_SECRET", getString(cfgMap, "app_secret"), "FEISHU_APP_SECRET")
	encryptKey := resolveValue(account, "ENCRYPT_KEY", getString(cfgMap, "encrypt_key"), "FEISHU_ENCRYPT_KEY")
	verificationToken := resolveValue(account, "VERIFICATION_TOKEN", getString(cfgMap, "verification_token"), "FEISHU_VERIFICATION_TOKEN")
	botOpenID := resolveValue(account, "BOT_OPEN_ID", getString(cfgMap, "bot_open_id"), "FEISHU_BOT_OPEN_ID")

	botName := strings.TrimSpace(firstNonEmpty(accountEnv(account, "BOT_NAME"), getString(cfgMap, "bot_name"), os.Getenv("FEISHU_BOT_NAME")))
	replyPrefix := firstNonEmpty(accountEnv(account, "REPLY_PREFIX"), getString(cfgMap, "reply_prefix"), os.Getenv("FEISHU_REPLY_PREFIX"))

	requireMention := resolveBool(
		accountEnv(account, "REQUIRE_MENTION"),
		getValue(cfgMap, "require_mention"),
		os.Getenv("FEISHU_REQUIRE_MENTION"),
		true,
	)
	requireMentionGroupOnly := resolveBool(
		accountEnv(account, "REQUIRE_MENTION_GROUP_ONLY"),
		getValue(cfgMap, "require_mention_group_only"),
		os.Getenv("FEISHU_REQUIRE_MENTION_GROUP_ONLY"),
		true,
	)
	mentionAliases := resolveMentionAliases(botName, getStringSlice(cfgMap, "mention_aliases"), replyPrefix, getNestedString(cfgMap, "codex", "system_prompt"), getNestedString(cfgMap, "progress", "doc", "title_prefix"))

	codex := resolveCodexConfig(account, cfgMap)
	speech := resolveSpeechConfig(account, cfgMap, codex)

	return Config{
		RepoRoot:                repoRoot,
		AccountName:             account,
		Config:                  cfgMap,
		ConfigPath:              store.OverlayTOMLPath(),
		DomainLabel:             domainLabel,
		DomainBaseURL:           domainBaseURL,
		AppID:                   appID,
		AppSecret:               appSecret,
		EncryptKey:              encryptKey,
		VerificationToken:       verificationToken,
		BotOpenID:               botOpenID,
		AutoReply:               resolveBool(accountEnv(account, "AUTO_REPLY"), getValue(cfgMap, "auto_reply"), os.Getenv("FEISHU_AUTO_REPLY"), true),
		IgnoreSelf:              resolveBool(accountEnv(account, "IGNORE_SELF_MESSAGES"), getValue(cfgMap, "ignore_self_messages"), os.Getenv("FEISHU_IGNORE_SELF_MESSAGES"), true),
		BotName:                 botName,
		ReplyPrefix:             replyPrefix,
		ReplyMode:               replyMode,
		RequireMention:          requireMention,
		RequireMentionGroupOnly: requireMentionGroupOnly,
		MentionAliases:          mentionAliases,
		TypingIndicatorEnabled: resolveBool(
			accountEnv(account, "TYPING_INDICATOR"),
			getNestedValue(cfgMap, "typing_indicator", "enabled"),
			os.Getenv("FEISHU_TYPING_INDICATOR"),
			true,
		),
		TypingEmoji: strings.TrimSpace(firstNonEmpty(
			accountEnv(account, "TYPING_EMOJI"),
			getNestedString(cfgMap, "typing_indicator", "emoji"),
			os.Getenv("FEISHU_TYPING_EMOJI"),
			"Typing",
		)),
		FakeStreamEnabled: resolveBool(
			accountEnv(account, "FAKE_STREAM"),
			getNestedValue(cfgMap, "fake_stream", "enabled"),
			os.Getenv("FEISHU_FAKE_STREAM"),
			false,
		),
		FakeStreamIntervalMS: resolveInt(
			accountEnv(account, "FAKE_STREAM_INTERVAL_MS"),
			getNestedValue(cfgMap, "fake_stream", "interval_ms"),
			os.Getenv("FEISHU_FAKE_STREAM_INTERVAL_MS"),
			120,
		),
		FakeStreamChunkChars: resolveInt(
			accountEnv(account, "FAKE_STREAM_CHUNK_CHARS"),
			getNestedValue(cfgMap, "fake_stream", "chunk_chars"),
			os.Getenv("FEISHU_FAKE_STREAM_CHUNK_CHARS"),
			1,
		),
		FakeStreamMaxUpdates: resolveInt(
			accountEnv(account, "FAKE_STREAM_MAX_UPDATES"),
			getNestedValue(cfgMap, "fake_stream", "max_updates"),
			os.Getenv("FEISHU_FAKE_STREAM_MAX_UPDATES"),
			120,
		),
		Progress: ProgressConfig{
			Enabled: resolveBool(
				accountEnv(account, "PROGRESS_ENABLED"),
				getNestedValue(cfgMap, "progress", "enabled"),
				firstNonEmpty(os.Getenv(accountEnvKey(account, "PROGRESS_NOTICE")), os.Getenv("FEISHU_PROGRESS_ENABLED"), os.Getenv("FEISHU_PROGRESS_NOTICE")),
				true,
			),
			Message: strings.TrimSpace(firstNonEmpty(
				accountEnv(account, "PROGRESS_MESSAGE"),
				getNestedString(cfgMap, "progress", "message"),
				os.Getenv("FEISHU_PROGRESS_MESSAGE"),
				"已接收，正在执行。",
			)),
			Mode: strings.TrimSpace(firstNonEmpty(
				accountEnv(account, "PROGRESS_MODE"),
				getNestedString(cfgMap, "progress", "mode"),
				os.Getenv("FEISHU_PROGRESS_MODE"),
				"doc",
			)),
			Doc: ProgressDocConfig{
				TitlePrefix: strings.TrimSpace(firstNonEmpty(
					accountEnv(account, "PROGRESS_DOC_TITLE_PREFIX"),
					getNestedString(cfgMap, "progress", "doc", "title_prefix"),
					os.Getenv("FEISHU_PROGRESS_DOC_TITLE_PREFIX"),
					"AI 助手｜任务进度",
				)),
				ShareToChat: resolveBool(
					accountEnv(account, "PROGRESS_DOC_SHARE_TO_CHAT"),
					getNestedValue(cfgMap, "progress", "doc", "share_to_chat"),
					os.Getenv("FEISHU_PROGRESS_DOC_SHARE_TO_CHAT"),
					true,
				),
				LinkScope: strings.TrimSpace(firstNonEmpty(
					accountEnv(account, "PROGRESS_DOC_LINK_SCOPE"),
					getNestedString(cfgMap, "progress", "doc", "link_scope"),
					os.Getenv("FEISHU_PROGRESS_DOC_LINK_SCOPE"),
					"same_tenant",
				)),
				IncludeUserMessage: resolveBool(
					accountEnv(account, "PROGRESS_DOC_INCLUDE_USER_MESSAGE"),
					getNestedValue(cfgMap, "progress", "doc", "include_user_message"),
					os.Getenv("FEISHU_PROGRESS_DOC_INCLUDE_USER_MESSAGE"),
					true,
				),
				WriteFinalReply: resolveBool(
					accountEnv(account, "PROGRESS_DOC_WRITE_FINAL_REPLY"),
					getNestedValue(cfgMap, "progress", "doc", "write_final_reply"),
					os.Getenv("FEISHU_PROGRESS_DOC_WRITE_FINAL_REPLY"),
					true,
				),
			},
		},
		Codex:  codex,
		Speech: speech,
	}, nil
}

func resolveCodexConfig(account string, cfg map[string]any) CodexConfig {
	codexCfg := getMap(cfg, "codex")
	apiKey := resolveValueFromCandidates([]resolvedCandidate{
		{Source: "env_account", Value: accountEnv(account, "CODEX_API_KEY")},
		{Source: "env", Value: os.Getenv("FEISHU_CODEX_API_KEY")},
		{Source: "config", Value: getString(codexCfg, "api_key")},
		{Source: "env", Value: os.Getenv("CODEX_API_KEY")},
		{Source: "env_openai", Value: os.Getenv("OPENAI_API_KEY")},
	})
	sandbox := strings.TrimSpace(firstNonEmpty(accountEnv(account, "CODEX_SANDBOX"), getString(codexCfg, "sandbox"), os.Getenv("FEISHU_CODEX_SANDBOX"), "danger-full-access"))
	if sandbox == "" {
		sandbox = "danger-full-access"
	}
	approval := strings.TrimSpace(firstNonEmpty(accountEnv(account, "CODEX_APPROVAL_POLICY"), getString(codexCfg, "approval_policy"), os.Getenv("FEISHU_CODEX_APPROVAL_POLICY"), "never"))
	if approval == "" {
		approval = "never"
	}
	return CodexConfig{
		Bin:             strings.TrimSpace(firstNonEmpty(accountEnv(account, "CODEX_BIN"), os.Getenv("CODEX_BIN"), os.Getenv("FEISHU_CODEX_BIN"), getString(codexCfg, "bin"), "codex")),
		Model:           strings.TrimSpace(firstNonEmpty(accountEnv(account, "CODEX_MODEL"), getString(codexCfg, "model"), os.Getenv("FEISHU_CODEX_MODEL"))),
		ReasoningEffort: strings.TrimSpace(firstNonEmpty(accountEnv(account, "CODEX_REASONING_EFFORT"), getString(codexCfg, "reasoning_effort"), os.Getenv("FEISHU_CODEX_REASONING_EFFORT"))),
		Profile:         strings.TrimSpace(firstNonEmpty(accountEnv(account, "CODEX_PROFILE"), getString(codexCfg, "profile"), os.Getenv("FEISHU_CODEX_PROFILE"))),
		Cwd: resolveOptionalDir(firstNonEmpty(
			accountEnv(account, "CODEX_CWD"),
			os.Getenv(accountEnvKey(account, "CODEX_CD")),
			os.Getenv("FEISHU_CODEX_CWD"),
			os.Getenv("FEISHU_CODEX_CD"),
			getString(codexCfg, "cwd"),
			getString(codexCfg, "cd"),
		)),
		AddDirs: resolveOptionalDirList(firstNonEmpty(
			accountEnv(account, "CODEX_ADD_DIRS"),
			getStringSliceJoined(codexCfg, "add_dirs"),
			os.Getenv("FEISHU_CODEX_ADD_DIRS"),
		)),
		HistoryTurns: resolveInt(accountEnv(account, "HISTORY_TURNS"), getValue(codexCfg, "history_turns"), os.Getenv("FEISHU_HISTORY_TURNS"), 6),
		SystemPrompt: strings.TrimSpace(firstNonEmpty(accountEnv(account, "CODEX_SYSTEM_PROMPT"), getString(codexCfg, "system_prompt"), os.Getenv("FEISHU_CODEX_SYSTEM_PROMPT"), defaultCodexSystemPrompt)),
		APIKey:       apiKey.Value,
		APIKeySource: apiKey.Source,
		BaseURL: strings.TrimRight(strings.TrimSpace(firstNonEmpty(
			accountEnv(account, "CODEX_BASE_URL"),
			os.Getenv("FEISHU_CODEX_BASE_URL"),
			getString(codexCfg, "base_url"),
			os.Getenv("OPENAI_BASE_URL"),
			os.Getenv("OPENAI_API_BASE"),
		)), "/"),
		Sandbox:        sandbox,
		ApprovalPolicy: approval,
	}
}

func resolveSpeechConfig(account string, cfg map[string]any, codex CodexConfig) SpeechConfig {
	speechCfg := getMap(cfg, "speech")
	apiKey := resolveValueFromCandidates([]resolvedCandidate{
		{Source: "env_account", Value: accountEnv(account, "SPEECH_API_KEY")},
		{Source: "env", Value: os.Getenv("FEISHU_SPEECH_API_KEY")},
		{Source: "config", Value: getString(speechCfg, "api_key")},
		{Source: "env_openai", Value: os.Getenv("OPENAI_API_KEY")},
		{Source: "env_codex", Value: os.Getenv("CODEX_API_KEY")},
		{Source: codex.APIKeySource, Value: codex.APIKey},
	})
	ffmpeg := resolveFFmpeg(account, cfg)
	return SpeechConfig{
		Enabled:      resolveBool(accountEnv(account, "SPEECH_ENABLED"), getValue(speechCfg, "enabled"), os.Getenv("FEISHU_SPEECH_ENABLED"), true),
		Model:        strings.TrimSpace(firstNonEmpty(accountEnv(account, "SPEECH_MODEL"), getString(speechCfg, "model"), os.Getenv("FEISHU_SPEECH_MODEL"), "gpt-4o-mini-transcribe")),
		Language:     strings.TrimSpace(firstNonEmpty(accountEnv(account, "SPEECH_LANGUAGE"), getString(speechCfg, "language"), os.Getenv("FEISHU_SPEECH_LANGUAGE"))),
		APIKey:       apiKey.Value,
		APIKeySource: apiKey.Source,
		BaseURL: strings.TrimRight(strings.TrimSpace(firstNonEmpty(
			accountEnv(account, "SPEECH_BASE_URL"),
			os.Getenv("FEISHU_SPEECH_BASE_URL"),
			getString(speechCfg, "base_url"),
			os.Getenv("OPENAI_BASE_URL"),
			os.Getenv("OPENAI_API_BASE"),
			"https://api.openai.com/v1",
		)), "/"),
		FFmpegBin:     ffmpeg.Bin,
		FFmpegVersion: ffmpeg.Version,
	}
}

type detectedBinary struct {
	Bin     string
	Found   bool
	Version string
}

func resolveFFmpeg(account string, cfg map[string]any) detectedBinary {
	speechCfg := getMap(cfg, "speech")
	candidates := uniqueStrings(
		accountEnv(account, "SPEECH_FFMPEG_BIN"),
		os.Getenv("FEISHU_SPEECH_FFMPEG_BIN"),
		getString(speechCfg, "ffmpeg_bin"),
		"ffmpeg",
	)
	for _, bin := range candidates {
		found, version := DetectBinary(bin, "-version")
		if found {
			return detectedBinary{Bin: bin, Found: true, Version: version}
		}
	}
	return detectedBinary{}
}

type resolvedCandidate struct {
	Source string
	Value  string
}

func resolveValue(account, suffix, configValue string, envFallback string) ResolvedValue {
	return resolveValueFromCandidates([]resolvedCandidate{
		{Source: "env_account", Value: accountEnv(account, suffix)},
		{Source: "config", Value: configValue},
		{Source: "env", Value: os.Getenv(envFallback)},
	})
}

func resolveValueFromCandidates(candidates []resolvedCandidate) ResolvedValue {
	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate.Value)
		if value == "" {
			continue
		}
		return ResolvedValue{Value: value, Source: candidate.Source}
	}
	return ResolvedValue{}
}

func resolveDomain(raw string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "feishu":
		return "feishu", lark.FeishuBaseUrl, nil
	case "lark":
		return "lark", lark.LarkBaseUrl, nil
	default:
		if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
			return strings.TrimRight(raw, "/"), strings.TrimRight(raw, "/"), nil
		}
		return "", "", fmt.Errorf("invalid domain %q, expected feishu | lark | https://open.xxx.com", raw)
	}
}

func applyDerivedBotConfig(account string, cfg map[string]any) map[string]any {
	out := configstore.DeepMerge(cfg)
	account = strings.TrimSpace(account)
	if account == "" || account == "default" {
		return out
	}
	codexCfg := getMap(out, "codex")
	cwd := strings.TrimSpace(getString(codexCfg, "cwd"))
	root := strings.TrimSpace(getString(codexCfg, "cwd_root"))
	if cwd == "" && root != "" {
		derived := configstore.AccountDirName(account)
		if derived == "" {
			derived = account
		}
		codexCfg["cwd"] = filepath.ToSlash(filepath.Join(root, derived))
		out["codex"] = codexCfg
	}
	return out
}

func accountEnv(account, suffix string) string {
	return os.Getenv(accountEnvKey(account, suffix))
}

func accountEnvKey(account, suffix string) string {
	account = strings.TrimSpace(account)
	suffix = strings.TrimSpace(suffix)
	if account == "" || suffix == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("FEISHU_")
	for _, r := range account {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - ('a' - 'A'))
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	b.WriteByte('_')
	b.WriteString(suffix)
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func resolveBool(values ...any) bool {
	if len(values) == 0 {
		return false
	}
	def := false
	if last, ok := values[len(values)-1].(bool); ok {
		def = last
		values = values[:len(values)-1]
	}
	for _, value := range values {
		switch v := value.(type) {
		case bool:
			return v
		case string:
			text := strings.TrimSpace(strings.ToLower(v))
			if text == "" {
				continue
			}
			switch text {
			case "1", "true", "yes", "y", "on":
				return true
			case "0", "false", "no", "n", "off":
				return false
			}
		case int:
			return v != 0
		}
	}
	return def
}

func resolveInt(a any, b any, c string, def int) int {
	for _, value := range []any{a, b, c} {
		switch v := value.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		case string:
			text := strings.TrimSpace(v)
			if text == "" {
				continue
			}
			if parsed, err := strconv.Atoi(text); err == nil {
				return parsed
			}
		}
	}
	return def
}

func getValue(root map[string]any, parts ...string) any {
	cur := any(root)
	for _, part := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		next, ok := m[part]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func getMap(root map[string]any, parts ...string) map[string]any {
	value := getValue(root, parts...)
	m, _ := value.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return configstore.DeepMerge(m)
}

func getString(root map[string]any, parts ...string) string {
	value := getValue(root, parts...)
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func getNestedString(root map[string]any, parts ...string) string {
	return getString(root, parts...)
}

func getNestedValue(root map[string]any, parts ...string) any {
	return getValue(root, parts...)
}

func getStringSlice(root map[string]any, parts ...string) []string {
	value := getValue(root, parts...)
	switch v := value.(type) {
	case []string:
		return uniqueStrings(v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return uniqueStrings(out...)
	case string:
		return uniqueStrings(strings.FieldsFunc(v, func(r rune) bool {
			return r == ',' || r == '\n'
		})...)
	default:
		return nil
	}
}

func getStringSliceJoined(root map[string]any, parts ...string) string {
	return strings.Join(getStringSlice(root, parts...), string(os.PathListSeparator))
}

func resolveOptionalDir(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	if filepath.IsAbs(text) {
		return filepath.Clean(text)
	}
	abs, err := filepath.Abs(text)
	if err != nil {
		return filepath.Clean(text)
	}
	return abs
}

func resolveOptionalDirList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	splitter := func(r rune) bool {
		return r == '\n' || r == ',' || r == rune(os.PathListSeparator)
	}
	parts := strings.FieldsFunc(raw, splitter)
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		dir := resolveOptionalDir(part)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}

func uniqueStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		text := strings.TrimSpace(value)
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		out = append(out, text)
	}
	return out
}

func resolveMentionAliases(botName string, explicit []string, replyPrefix string, _ string, progressTitlePrefix string) []string {
	aliases := uniqueStrings(botName, replyPrefix, progressTitlePrefix)
	aliases = append(aliases, explicit...)
	for idx, item := range aliases {
		aliases[idx] = normalizeAlias(item)
	}
	aliases = uniqueStrings(aliases...)
	sort.Slice(aliases, func(i, j int) bool { return len([]rune(aliases[i])) > len([]rune(aliases[j])) })
	return aliases
}

func normalizeAlias(raw string) string {
	text := strings.TrimSpace(strings.ReplaceAll(raw, "\u00a0", " "))
	text = strings.Trim(text, "@＠")
	text = strings.TrimSpace(text)
	text = strings.TrimRight(text, ":：,，;；、.!！?？")
	return strings.TrimSpace(text)
}

const defaultCodexSystemPrompt = `你是运行在飞书中的 SunCodexClaw 助手。
直接解决问题，避免空话。
能自己查就先自己查，能自己做就先自己做。
最终回复直接面向用户，不要复述任务描述，不要承诺“稍后回复”。`
