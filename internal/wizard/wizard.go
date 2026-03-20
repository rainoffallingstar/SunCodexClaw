package wizard

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"suncodexclaw/internal/configstore"
)

type inspectResult struct {
	Account string `json:"account"`
	Repo    string `json:"repo"`
	Paths   struct {
		AccountJSON string `json:"accountJson"`
	} `json:"paths"`
	Items []missingItem `json:"items"`
}

type missingItem struct {
	Key         string `json:"key"`
	Prompt      string `json:"prompt"`
	Recommended string `json:"recommended"`
	Optional    bool   `json:"optional"`
	Type        string `json:"type"`   // string|bool|int|string_list
	Target      string `json:"target"` // secrets|overlay
}

type applyRequest struct {
	Secrets       map[string]any `json:"secrets"`
	Overlay       map[string]any `json:"overlay"`
	SharedOverlay map[string]any `json:"sharedOverlay"`
}

type Options struct {
	Args []string
}

func Usage(w io.Writer, bin string) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintf(w, "  %s configure --account assistant [--yes]\n", bin)
	fmt.Fprintf(w, "  %s configure add --account reviewer [--yes]\n", bin)
}

func Configure(opts Options) error {
	args := append([]string{}, opts.Args...)
	if len(args) > 0 && args[0] == "add" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	account := fs.String("account", "", "feishu account name (config/feishu/bots.toml [bot.<account>])")
	yes := fs.Bool("yes", false, "accept current or recommended defaults without prompting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*account) == "" {
		return fmt.Errorf("configure requires --account <account>")
	}

	root, err := findRepoRoot()
	if err != nil {
		return err
	}

	store := configstore.NewStore(root)
	inspect, effective, err := inspectConfig(store, *account)
	if err != nil {
		return err
	}
	shared, err := store.ReadOverlay("default")
	if err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)
	secretsPatch := map[string]any{}
	overlayPatch := map[string]any{}
	sharedOverlayPatch := map[string]any{}

	fmt.Println("SunCodexClaw config wizard")
	fmt.Printf("account=%s\n", inspect.Account)
	fmt.Println("Press Enter to keep the current value or accept the suggested default.")
	fmt.Println("")

	sharedRootItem := missingItem{
		Key:         "codex.cwd_root",
		Prompt:      "Shared Codex workspace root path",
		Recommended: firstNonEmpty(renderPromptDefault(shared, "codex.cwd_root"), "workspace"),
		Optional:    true,
		Target:      "overlay",
	}
	if val, ok, err := askForItem(reader, sharedRootItem, *yes); err != nil {
		return err
	} else if ok {
		setDotted(sharedOverlayPatch, "codex.cwd_root", val)
	}

	for _, group := range groupMissing(inspect.Items) {
		fmt.Printf("== %s ==\n", group.name)
		for _, item := range group.items {
			promptItem := item
			promptItem.Recommended = firstNonEmpty(renderPromptDefault(effective, item.Key), item.Recommended)
			val, ok, askErr := askForItem(reader, promptItem, *yes)
			if askErr != nil {
				return askErr
			}
			if !ok {
				continue
			}

			patch := overlayPatch
			if item.Target == "secrets" {
				patch = secretsPatch
			}
			setDotted(patch, item.Key, val)
		}
		fmt.Println("")
	}

	req := applyRequest{Secrets: secretsPatch, Overlay: overlayPatch, SharedOverlay: sharedOverlayPatch}
	if err := applyPatches(store, *account, req); err != nil {
		return err
	}

	fmt.Println("")
	fmt.Println("Done.")
	fmt.Printf("updated=config/secrets/local.toml\n")
	fmt.Printf("updated=config/feishu/bots.toml [shared]\n")
	fmt.Printf("updated=%s\n", inspect.Paths.AccountJSON)
	return nil
}

func prompt(r *bufio.Reader, question, def string) (string, error) {
	if def != "" {
		fmt.Printf("%s (default: %s): ", question, def)
	} else {
		fmt.Printf("%s: ", question)
	}
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	v := strings.TrimSpace(line)
	if v == "" {
		return def, nil
	}
	return v, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func renderPromptDefault(effective map[string]any, dotted string) string {
	value, ok := getDottedValue(effective, dotted)
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%.0f", v)
	case []string:
		return strings.Join(v, ",")
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, strings.TrimSpace(fmt.Sprintf("%v", item)))
		}
		return strings.Join(out, ",")
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func getDottedValue(root map[string]any, dotted string) (any, bool) {
	if root == nil {
		return nil, false
	}
	parts := strings.Split(dotted, ".")
	var cur any = root
	for _, part := range parts {
		next, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = next[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

type missingGroup struct {
	name  string
	items []missingItem
}

func groupMissing(items []missingItem) []missingGroup {
	var feishuCreds, bot, progress, codex, sync []missingItem
	for _, it := range items {
		switch {
		case it.Target == "secrets" && (it.Key == "app_id" || it.Key == "app_secret" || it.Key == "encrypt_key" || it.Key == "verification_token"):
			feishuCreds = append(feishuCreds, it)
		case strings.HasPrefix(it.Key, "progress."):
			progress = append(progress, it)
		case strings.HasPrefix(it.Key, "codex."):
			codex = append(codex, it)
		case strings.HasPrefix(it.Key, "sync."):
			sync = append(sync, it)
		default:
			bot = append(bot, it)
		}
	}

	var out []missingGroup
	if len(feishuCreds) > 0 {
		out = append(out, missingGroup{name: "Feishu Credentials (config/secrets/local.toml [feishu.<account>])", items: feishuCreds})
	}
	if len(bot) > 0 {
		out = append(out, missingGroup{name: "Bot Settings (config/feishu/bots.toml [bot.<account>])", items: bot})
	}
	if len(progress) > 0 {
		out = append(out, missingGroup{name: "Progress Settings (config/feishu/bots.toml [bot.<account>])", items: progress})
	}
	if len(codex) > 0 {
		out = append(out, missingGroup{name: "Codex Settings", items: codex})
	}
	if len(sync) > 0 {
		out = append(out, missingGroup{name: "Sync Backup Settings (config/secrets/local.toml [sync.<account>])", items: sync})
	}
	return out
}

func askForItem(r *bufio.Reader, item missingItem, useYes bool) (any, bool, error) {
	def := strings.TrimSpace(item.Recommended)
	if useYes {
		if !item.Optional && def == "" {
			return nil, false, fmt.Errorf("missing required field with no default: %s", item.Key)
		}
		if item.Optional && def == "" {
			return nil, false, nil
		}
		return coerce(item, def)
	}

	q := item.Prompt
	if item.Optional {
		q = q + " (optional)"
	}
	raw, err := prompt(r, q, def)
	if err != nil {
		return nil, false, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" && item.Optional {
		return nil, false, nil
	}
	return coerce(item, raw)
}

func coerce(item missingItem, raw string) (any, bool, error) {
	switch item.Type {
	case "bool":
		v, ok, err := parseBool(raw)
		if err != nil {
			return nil, false, err
		}
		if !ok && item.Optional {
			return nil, false, nil
		}
		return v, true, nil
	case "int":
		v, err := parseInt(raw)
		if err != nil {
			return nil, false, err
		}
		return v, true, nil
	case "string_list":
		list := parseList(raw)
		if len(list) == 0 && item.Optional {
			return nil, false, nil
		}
		return list, true, nil
	default:
		if strings.TrimSpace(raw) == "" && item.Optional {
			return nil, false, nil
		}
		return raw, true, nil
	}
}

func parseBool(s string) (bool, bool, error) {
	v := strings.TrimSpace(strings.ToLower(s))
	if v == "" {
		return false, false, nil
	}
	switch v {
	case "1", "true", "yes", "y", "on":
		return true, true, nil
	case "0", "false", "no", "n", "off":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("invalid bool: %q (use true/false)", s)
	}
}

func parseInt(s string) (int, error) {
	v := strings.TrimSpace(s)
	var n int
	_, err := fmt.Sscanf(v, "%d", &n)
	if err != nil {
		return 0, fmt.Errorf("invalid int: %q", s)
	}
	return n, nil
}

func parseList(s string) []string {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func setDotted(root map[string]any, dotted string, val any) {
	parts := strings.Split(dotted, ".")
	if len(parts) == 1 {
		root[dotted] = val
		return
	}
	cur := root
	for i := 0; i < len(parts)-1; i++ {
		p := parts[i]
		next, ok := cur[p]
		if !ok {
			m := map[string]any{}
			cur[p] = m
			cur = m
			continue
		}
		m, ok := next.(map[string]any)
		if !ok {
			m = map[string]any{}
			cur[p] = m
		}
		cur = m
	}
	cur[parts[len(parts)-1]] = val
}

func inspectConfig(store *configstore.Store, account string) (*inspectResult, map[string]any, error) {
	overlay, err := store.ReadOverlay(account)
	if err != nil {
		return nil, nil, err
	}
	secrets, err := store.ReadSecretsEntry("feishu", account)
	if err != nil {
		return nil, nil, err
	}
	syncDefault, err := store.ReadSecretsEntry("sync", "default")
	if err != nil {
		return nil, nil, err
	}
	syncAccount, err := store.ReadSecretsEntry("sync", account)
	if err != nil {
		return nil, nil, err
	}
	effective := configstore.DeepMerge(secrets, overlay, map[string]any{
		"sync": configstore.DeepMerge(syncDefault, syncAccount),
	})

	res := &inspectResult{Account: account, Repo: store.RepoRoot}
	res.Paths.AccountJSON = store.OverlayTargetLabel(account)
	res.Items = buildItems(account)
	return res, effective, nil
}

func applyPatches(store *configstore.Store, account string, req applyRequest) error {
	if len(req.Secrets) > 0 {
		feishuPatch := stripTopLevel(req.Secrets, "sync")
		syncPatch := getTopLevelMap(req.Secrets, "sync")
		if len(feishuPatch) > 0 {
			if _, err := store.UpsertSecretsEntry("feishu", account, feishuPatch); err != nil {
				return err
			}
		}
		if len(syncPatch) > 0 {
			if _, err := store.UpsertSecretsEntry("sync", account, syncPatch); err != nil {
				return err
			}
		}
	}
	if len(req.SharedOverlay) > 0 {
		if err := store.WriteOverlay("default", req.SharedOverlay); err != nil {
			return err
		}
	}
	if len(req.Overlay) > 0 {
		if err := store.WriteOverlay(account, req.Overlay); err != nil {
			return err
		}
	}
	return nil
}

func stripTopLevel(root map[string]any, key string) map[string]any {
	out := map[string]any{}
	for k, v := range root {
		if k == key {
			continue
		}
		out[k] = v
	}
	return out
}

func getTopLevelMap(root map[string]any, key string) map[string]any {
	v, ok := root[key]
	if !ok {
		return map[string]any{}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return m
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 20; i++ {
		if fileExists(filepath.Join(dir, "package.json")) && fileExists(filepath.Join(dir, "tools")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("repo root not found (expected package.json + tools/)")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func buildItems(account string) []missingItem {
	items := []missingItem{}
	add := func(item missingItem) {
		items = append(items, item)
	}

	// Feishu secrets
	add(missingItem{Key: "app_id", Prompt: "Feishu app_id", Target: "secrets"})
	add(missingItem{Key: "app_secret", Prompt: "Feishu app_secret", Target: "secrets"})
	add(missingItem{Key: "encrypt_key", Prompt: "Feishu encrypt_key", Target: "secrets"})
	add(missingItem{Key: "verification_token", Prompt: "Feishu verification_token", Target: "secrets"})

	// Bot identity
	add(missingItem{Key: "enabled", Prompt: "Bot enabled (true/false)", Recommended: "true", Optional: true, Type: "bool", Target: "overlay"})
	add(missingItem{Key: "bot_name", Prompt: "Bot name", Recommended: defaultBotName(account), Optional: true, Target: "overlay"})
	add(missingItem{Key: "domain", Prompt: "Feishu domain (feishu|lark)", Recommended: "feishu", Optional: true, Target: "overlay"})
	add(missingItem{Key: "reply_mode", Prompt: "Reply mode (codex)", Recommended: "codex", Optional: true, Target: "overlay"})
	add(missingItem{Key: "reply_prefix", Prompt: "Reply prefix", Recommended: "AI 助手：", Optional: true, Target: "overlay"})
	add(missingItem{Key: "require_mention", Prompt: "Require mention in groups (true/false)", Recommended: "true", Optional: true, Type: "bool", Target: "overlay"})
	add(missingItem{Key: "require_mention_group_only", Prompt: "Require mention group-only (true/false)", Recommended: "true", Optional: true, Type: "bool", Target: "overlay"})
	add(missingItem{Key: "mention_aliases", Prompt: "Mention aliases (comma/newline separated, optional)", Optional: true, Type: "string_list", Target: "overlay"})
	add(missingItem{Key: "ignore_self_messages", Prompt: "Ignore self messages (true/false)", Recommended: "true", Optional: true, Type: "bool", Target: "overlay"})
	add(missingItem{Key: "auto_reply", Prompt: "Auto reply enabled (true/false)", Recommended: "true", Optional: true, Type: "bool", Target: "overlay"})

	// Progress (overlay)
	add(missingItem{Key: "progress.enabled", Prompt: "Enable progress notice (true/false)", Recommended: "true", Optional: true, Type: "bool", Target: "overlay"})
	add(missingItem{Key: "progress.message", Prompt: "Progress message (when enabled)", Recommended: "已接收，正在执行。", Optional: true, Target: "overlay"})
	add(missingItem{Key: "progress.mode", Prompt: "Progress mode (doc/message)", Recommended: "doc", Optional: true, Target: "overlay"})
	add(missingItem{Key: "progress.doc.title_prefix", Prompt: "Progress doc title prefix", Recommended: "AI 助手｜任务进度", Optional: true, Target: "overlay"})
	add(missingItem{Key: "progress.doc.share_to_chat", Prompt: "Progress doc share_to_chat (true/false)", Recommended: "true", Optional: true, Type: "bool", Target: "overlay"})
	add(missingItem{Key: "progress.doc.link_scope", Prompt: "Progress doc link_scope (same_tenant/anyone/closed)", Recommended: "same_tenant", Optional: true, Target: "overlay"})
	add(missingItem{Key: "progress.doc.include_user_message", Prompt: "Progress doc include_user_message (true/false)", Recommended: "true", Optional: true, Type: "bool", Target: "overlay"})
	add(missingItem{Key: "progress.doc.write_final_reply", Prompt: "Progress doc write_final_reply (true/false)", Recommended: "true", Optional: true, Type: "bool", Target: "overlay"})

	// Typing (overlay)
	add(missingItem{Key: "typing_indicator.enabled", Prompt: "Typing indicator enabled (true/false)", Recommended: "true", Optional: true, Type: "bool", Target: "overlay"})
	add(missingItem{Key: "typing_indicator.emoji", Prompt: "Typing indicator emoji (Feishu reaction type)", Recommended: "Typing", Optional: true, Target: "overlay"})

	// Fake stream (overlay)
	add(missingItem{Key: "fake_stream.enabled", Prompt: "Fake stream enabled (true/false)", Recommended: "false", Optional: true, Type: "bool", Target: "overlay"})
	add(missingItem{Key: "fake_stream.interval_ms", Prompt: "Fake stream interval ms", Recommended: "120", Optional: true, Type: "int", Target: "overlay"})
	add(missingItem{Key: "fake_stream.chunk_chars", Prompt: "Fake stream chunk chars", Recommended: "1", Optional: true, Type: "int", Target: "overlay"})
	add(missingItem{Key: "fake_stream.max_updates", Prompt: "Fake stream max updates", Recommended: "120", Optional: true, Type: "int", Target: "overlay"})

	// Codex (overlay runtime)
	add(missingItem{Key: "codex.add_dirs", Prompt: "Codex additional dirs inside container (comma/newline separated)", Optional: true, Type: "string_list", Target: "overlay"})
	add(missingItem{Key: "codex.bin", Prompt: "Codex CLI binary name/path", Recommended: "codex", Optional: true, Target: "overlay"})
	add(missingItem{Key: "codex.model", Prompt: "Codex model (optional)", Optional: true, Target: "overlay"})
	add(missingItem{Key: "codex.reasoning_effort", Prompt: "Codex reasoning effort (optional)", Optional: true, Target: "overlay"})
	add(missingItem{Key: "codex.profile", Prompt: "Codex profile (optional)", Optional: true, Target: "overlay"})
	add(missingItem{Key: "codex.history_turns", Prompt: "Codex history turns (0-20)", Recommended: "6", Optional: true, Type: "int", Target: "overlay"})
	add(missingItem{Key: "codex.system_prompt", Prompt: "Codex system prompt (optional)", Optional: true, Target: "overlay"})
	add(missingItem{Key: "codex.sandbox", Prompt: "Codex sandbox (read-only/workspace-write/danger-full-access)", Recommended: "danger-full-access", Optional: true, Target: "overlay"})
	add(missingItem{Key: "codex.approval_policy", Prompt: "Codex approval policy (never/on-request/on-failure/untrusted)", Recommended: "never", Optional: true, Target: "overlay"})

	// Codex secrets/connection
	add(missingItem{Key: "codex.api_key", Prompt: "Codex/OpenAI API key (optional if codex already logged in)", Optional: true, Target: "secrets"})
	add(missingItem{Key: "codex.base_url", Prompt: "Codex base url (optional)", Optional: true, Target: "secrets"})

	// Speech
	add(missingItem{Key: "speech.enabled", Prompt: "Speech enabled (true/false)", Recommended: "true", Optional: true, Type: "bool", Target: "overlay"})
	add(missingItem{Key: "speech.api_key", Prompt: "Speech API key (optional; falls back to codex/openai key)", Optional: true, Target: "secrets"})
	add(missingItem{Key: "speech.model", Prompt: "Speech model", Recommended: "gpt-4o-mini-transcribe", Optional: true, Target: "overlay"})
	add(missingItem{Key: "speech.language", Prompt: "Speech language (optional, e.g. zh)", Optional: true, Target: "overlay"})
	add(missingItem{Key: "speech.base_url", Prompt: "Speech base url", Recommended: "https://api.openai.com/v1", Optional: true, Target: "overlay"})
	add(missingItem{Key: "speech.ffmpeg_bin", Prompt: "ffmpeg binary path (optional)", Optional: true, Target: "overlay"})

	// Sync backup (secrets)
	add(missingItem{Key: "sync.provider", Prompt: "Sync provider (webdav)", Recommended: "webdav", Optional: true, Target: "secrets"})
	add(missingItem{Key: "sync.workspace_id", Prompt: "Sync workspace id", Recommended: defaultSyncWorkspaceID(account), Optional: true, Target: "secrets"})
	add(missingItem{Key: "sync.webdav.url", Prompt: "WebDAV url", Optional: true, Target: "secrets"})
	add(missingItem{Key: "sync.webdav.username", Prompt: "WebDAV username", Optional: true, Target: "secrets"})
	add(missingItem{Key: "sync.webdav.password", Prompt: "WebDAV password", Optional: true, Target: "secrets"})
	add(missingItem{Key: "sync.webdav.base_path", Prompt: "WebDAV base path", Recommended: "/SunCodexClaw/backups", Optional: true, Target: "secrets"})

	return items
}

func defaultSyncWorkspaceID(account string) string {
	raw := strings.TrimSpace(account)
	if raw == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r == '/' || r == '\\' || r == ' ' || r == ':':
			b.WriteByte('-')
		case r == '.':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "default"
	}
	return out
}

func defaultBotName(account string) string {
	account = strings.TrimSpace(account)
	if account == "" {
		return "飞书 Codex 助手"
	}
	return "飞书 Codex 助手 " + account
}
