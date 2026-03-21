package configstore

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Store struct {
	RepoRoot  string
	ConfigDir string // config
}

func NewStore(repoRoot string) *Store {
	return &Store{
		RepoRoot:  repoRoot,
		ConfigDir: filepath.Join(repoRoot, "config"),
	}
}

func (s *Store) OverlayTOMLPath() string {
	return filepath.Join(s.ConfigDir, "feishu", "bots.toml")
}

func (s *Store) LocalTOMLPath() string {
	return filepath.Join(s.ConfigDir, "secrets", "local.toml")
}

func (s *Store) OverlayTargetLabel(account string) string {
	return s.OverlayTOMLPath() + " " + s.OverlayTableLabel(account)
}

func (s *Store) OverlayTableLabel(account string) string {
	if strings.TrimSpace(account) == "" || strings.TrimSpace(account) == "default" {
		return "[shared]"
	}
	return "[" + FormatTOMLPath("bot", account) + "]"
}

func (s *Store) SecretsEntryLabel(section, account string) string {
	return "[" + FormatTOMLPath(section, account) + "]"
}

func ResolveRuntimeAccountFromDir(dir, scope string) (string, string, error) {
	current := strings.TrimSpace(dir)
	if current == "" {
		return "", "", nil
	}
	if !filepath.IsAbs(current) {
		abs, err := filepath.Abs(current)
		if err != nil {
			return "", "", err
		}
		current = abs
	}
	scope = strings.TrimSpace(scope)
	for {
		cfgPath := filepath.Join(current, ".config.toml")
		if _, err := os.Stat(cfgPath); err == nil {
			doc, err := parseTOMLFile(cfgPath)
			if err != nil {
				return "", cfgPath, err
			}
			if runtimeMap, ok := getMapAt(doc.root, []string{"runtime"}); ok && scope != "" {
				key := scope + "_account"
				if value := strings.TrimSpace(getNestedRuntimeConfigString(runtimeMap, key)); value != "" {
					return value, cfgPath, nil
				}
			}
			if botMap, ok := getMapAt(doc.root, []string{"bot"}); ok {
				if value := strings.TrimSpace(getNestedRuntimeConfigString(botMap, "account")); value != "" {
					return value, cfgPath, nil
				}
			}
			return "", cfgPath, nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", cfgPath, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", "", nil
}

func AccountDirName(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\\", "-")
	text = strings.ReplaceAll(text, "/", "-")
	text = strings.ReplaceAll(text, " ", "-")
	text = strings.Trim(text, "-.")
	return text
}

func getNestedRuntimeConfigString(root *OMap, key string) string {
	if root == nil {
		return ""
	}
	value, ok := root.Get(key)
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	default:
		return ""
	}
}

func (s *Store) ReadOverlayDoc() (*tomlDoc, string, error) {
	p := s.OverlayTOMLPath()
	if _, err := os.Stat(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &tomlDoc{root: NewOMap()}, p, nil
		}
		return nil, "", err
	}
	doc, err := parseTOMLFile(p)
	return doc, p, err
}

func (s *Store) WriteOverlayDoc(doc *tomlDoc, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(doc.stringify()), 0o644)
}

func (s *Store) ReadOverlay(account string) (map[string]any, error) {
	doc, _, err := s.ReadOverlayDoc()
	if err != nil {
		return nil, err
	}
	shared := map[string]any{}
	if m, ok := getMapAt(doc.root, []string{"shared"}); ok {
		shared = toPlainMap(m)
	}
	accountMap := map[string]any{}
	if strings.TrimSpace(account) != "" && strings.TrimSpace(account) != "default" {
		if m, ok := getMapAt(doc.root, []string{"bot", account}); ok {
			accountMap = toPlainMap(m)
		}
	}
	return applyDerivedBotConfig(account, DeepMerge(shared, accountMap)), nil
}

func (s *Store) WriteOverlay(account string, patch map[string]any) error {
	doc, path, err := s.ReadOverlayDoc()
	if err != nil {
		return err
	}
	target := []string{"shared"}
	if strings.TrimSpace(account) != "" && strings.TrimSpace(account) != "default" {
		target = []string{"bot", account}
	}
	entry := ensureMapPath(doc.root, target)
	deepMergeInto(entry, patch)
	return s.WriteOverlayDoc(doc, path)
}

func (s *Store) ListOverlayAccountNames() ([]string, error) {
	names := map[string]bool{}
	doc, _, err := s.ReadOverlayDoc()
	if err != nil {
		return nil, err
	}
	if m, ok := getMapAt(doc.root, []string{"bot"}); ok {
		for _, k := range m.Keys() {
			names[k] = true
		}
	}
	out := []string{}
	for k := range names {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) ListConfiguredAccountNames() ([]string, error) {
	names := map[string]bool{}
	overlayNames, err := s.ListOverlayAccountNames()
	if err != nil {
		return nil, err
	}
	for _, name := range overlayNames {
		names[name] = true
	}
	secretNames, err := s.ListSecretsEntryNames("feishu")
	if err != nil {
		return nil, err
	}
	for _, name := range secretNames {
		if name == "default" || strings.HasSuffix(name, ".example") {
			continue
		}
		names[name] = true
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) ListEnabledAccountNames() ([]string, error) {
	names, err := s.ListConfiguredAccountNames()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		enabled, err := s.BotEnabled(name)
		if err != nil {
			return nil, err
		}
		if enabled {
			out = append(out, name)
		}
	}
	return out, nil
}

func (s *Store) BotEnabled(account string) (bool, error) {
	cfg, err := s.ReadOverlay(account)
	if err != nil {
		return false, err
	}
	value, ok := getNestedValueMap(cfg, "enabled")
	if !ok {
		return true, nil
	}
	return asBool(value, true), nil
}

func (s *Store) ReadSecretsDoc() (*tomlDoc, string, error) {
	p := s.LocalTOMLPath()
	if _, err := os.Stat(p); err == nil {
		doc, err := parseTOMLFile(p)
		return doc, p, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	return &tomlDoc{root: NewOMap()}, p, nil
}

func (s *Store) WriteSecretsDoc(doc *tomlDoc, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(doc.stringify()), 0o644)
}

func (s *Store) ReadSecretsEntry(section, account string) (map[string]any, error) {
	doc, _, err := s.ReadSecretsDoc()
	if err != nil {
		return nil, err
	}

	readFrom := func(root *OMap) map[string]any {
		if cfg, ok := getMapAt(root, []string{section, account}); ok {
			return toPlainMap(cfg)
		}
		return map[string]any{}
	}
	return readFrom(doc.root), nil
}

func (s *Store) UpsertSecretsEntry(section, account string, patch map[string]any) (string, error) {
	doc, path, err := s.ReadSecretsDoc()
	if err != nil {
		return "", err
	}
	entry := ensureMapPath(doc.root, []string{section, account})
	deepMergeInto(entry, patch)
	if err := s.WriteSecretsDoc(doc, path); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) ListSecretsEntryNames(section string) ([]string, error) {
	doc, _, err := s.ReadSecretsDoc()
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	if m, ok := getMapAt(doc.root, []string{section}); ok {
		for _, k := range m.Keys() {
			names[k] = true
		}
	}
	out := []string{}
	for k := range names {
		out = append(out, k)
	}
	return out, nil
}

func getMapAt(root *OMap, parts []string) (*OMap, bool) {
	cur := root
	for _, p := range parts {
		v, ok := cur.Get(p)
		if !ok {
			return nil, false
		}
		m, ok := v.(*OMap)
		if !ok {
			return nil, false
		}
		cur = m
	}
	return cur, true
}

func ensureMapPath(root *OMap, parts []string) *OMap {
	cur := root
	for _, p := range parts {
		if v, ok := cur.Get(p); ok {
			if m, ok := v.(*OMap); ok {
				cur = m
				continue
			}
		}
		n := NewOMap()
		cur.Set(p, n)
		cur = n
	}
	return cur
}

func toPlainMap(m *OMap) map[string]any {
	out := map[string]any{}
	if m == nil {
		return out
	}
	for _, k := range m.keys {
		v := m.values[k]
		switch tv := v.(type) {
		case *OMap:
			out[k] = toPlainMap(tv)
		case *[]any:
			out[k] = *tv
		default:
			out[k] = tv
		}
	}
	return out
}

func deepMergeInto(dst *OMap, patch map[string]any) {
	for k, v := range patch {
		if vMap, ok := v.(map[string]any); ok {
			existing, ok := dst.Get(k)
			if ok {
				if exMap, ok := existing.(*OMap); ok {
					deepMergeInto(exMap, vMap)
					continue
				}
			}
			n := NewOMap()
			deepMergeInto(n, vMap)
			dst.Set(k, n)
			continue
		}
		if vSlice, ok := v.([]any); ok {
			cp := make([]any, len(vSlice))
			copy(cp, vSlice)
			dst.Set(k, cp)
			continue
		}
		dst.Set(k, v)
	}
}

func DeepMerge(items ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, it := range items {
		for k, v := range it {
			switch tv := v.(type) {
			case map[string]any:
				cur, _ := out[k].(map[string]any)
				out[k] = DeepMerge(cur, tv)
			case []any:
				cp := make([]any, len(tv))
				copy(cp, tv)
				out[k] = cp
			default:
				out[k] = tv
			}
		}
	}
	return out
}

func applyDerivedBotConfig(account string, cfg map[string]any) map[string]any {
	out := DeepMerge(cfg)
	account = strings.TrimSpace(account)
	if account == "" || account == "default" {
		return out
	}
	codex := map[string]any{}
	if existing, ok := out["codex"].(map[string]any); ok {
		codex = DeepMerge(existing)
	}
	cwd := strings.TrimSpace(getNestedStringMap(out, "codex", "cwd"))
	root := strings.TrimSpace(getNestedStringMap(out, "codex", "cwd_root"))
	if cwd == "" && root != "" {
		derived := AccountDirName(account)
		if derived == "" {
			derived = account
		}
		codex["cwd"] = filepath.ToSlash(filepath.Join(root, derived))
	}
	if len(codex) > 0 {
		out["codex"] = codex
	}
	return out
}

func getNestedStringMap(m map[string]any, parts ...string) string {
	value, ok := getNestedValueMap(m, parts...)
	if !ok {
		return ""
	}
	v, _ := value.(string)
	return v
}

func getNestedValueMap(m map[string]any, parts ...string) (any, bool) {
	cur := any(m)
	for _, part := range parts {
		next, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next[part]
	}
	if cur == nil {
		return nil, false
	}
	return cur, true
}

func asBool(value any, def bool) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		default:
			return def
		}
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	default:
		return def
	}
}
