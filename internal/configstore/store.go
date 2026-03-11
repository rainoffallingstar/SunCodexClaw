package configstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Store struct {
	RepoRoot  string
	ConfigDir string // config/feishu
}

func NewStore(repoRoot string) *Store {
	return &Store{
		RepoRoot:  repoRoot,
		ConfigDir: filepath.Join(repoRoot, "config", "feishu"),
	}
}

func (s *Store) AccountJSONPath(account string) string {
	return filepath.Join(s.ConfigDir, account+".json")
}

func (s *Store) ReadOverlay(account string) (map[string]any, error) {
	p := s.AccountJSONPath(account)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func (s *Store) WriteOverlay(account string, patch map[string]any) error {
	cur, err := s.ReadOverlay(account)
	if err != nil {
		return err
	}
	next := DeepMerge(cur, patch)
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.AccountJSONPath(account)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.AccountJSONPath(account), append(b, '\n'), 0o644)
}

func (s *Store) ReadSecretsDoc() (*yamlDoc, string, error) {
	p := resolveSecretsFile(s.RepoRoot)
	if _, err := os.Stat(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &yamlDoc{root: NewOMap()}, p, nil
		}
		return nil, "", err
	}
	doc, err := parseYAMLFile(p)
	return doc, p, err
}

func (s *Store) WriteSecretsDoc(doc *yamlDoc, path string) error {
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

	// config.<section>.<account> is preferred
	if cfg, ok := getMapAt(doc.root, []string{"config", section, account}); ok {
		return toPlainMap(cfg), nil
	}
	// legacy: values.<section>.<account>
	if legacy, ok := getMapAt(doc.root, []string{"values", section, account}); ok {
		return toPlainMap(legacy), nil
	}
	return map[string]any{}, nil
}

func (s *Store) UpsertSecretsEntry(section, account string, patch map[string]any) (string, error) {
	doc, path, err := s.ReadSecretsDoc()
	if err != nil {
		return "", err
	}
	entry := ensureMapPath(doc.root, []string{"config", section, account})
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
	if m, ok := getMapAt(doc.root, []string{"config", section}); ok {
		for _, k := range m.Keys() {
			names[k] = true
		}
	}
	if m, ok := getMapAt(doc.root, []string{"values", section}); ok {
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
