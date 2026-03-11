package configstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalExampleRoundTrip(t *testing.T) {
	path := filepath.Join("..", "..", "config", "secrets", "local.example.yaml")
	origBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read local.example.yaml: %v", err)
	}
	orig := normalizeText(string(origBytes))

	doc, err := parseYAML(origBytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := normalizeText(doc.stringify())

	if out != orig {
		t.Fatalf("round-trip mismatch\n--- expected ---\n%s\n--- got ---\n%s", orig, out)
	}
}

func TestLegacyValuesLayoutRead(t *testing.T) {
	tmp := t.TempDir()
	secretsPath := filepath.Join(tmp, "local.yaml")
	body := strings.Join([]string{
		"values:",
		"  feishu:",
		"    assistant:",
		"      app_id: \"cli_legacy\"",
		"",
	}, "\n")
	if err := os.WriteFile(secretsPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Setenv("SUNCODEXCLAW_SECRETS_FILE", secretsPath)
	store := NewStore(tmp)

	entry, err := store.ReadSecretsEntry("feishu", "assistant")
	if err != nil {
		t.Fatalf("ReadSecretsEntry: %v", err)
	}
	if entry["app_id"] != "cli_legacy" {
		t.Fatalf("expected legacy app_id, got %#v", entry["app_id"])
	}
}

func TestNestedMapParsesChildren(t *testing.T) {
	src := strings.Join([]string{
		"root:",
		"  progress:",
		"    enabled: true",
		"    doc:",
		"      title: \"x\"",
		"",
	}, "\n")
	doc, err := parseYAML([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rootMap, ok := doc.root.Get("root")
	if !ok {
		t.Fatalf("missing root")
	}
	rootO, ok := rootMap.(*OMap)
	if !ok {
		t.Fatalf("root not map: %#v", rootMap)
	}
	progV, ok := rootO.Get("progress")
	if !ok {
		t.Fatalf("missing progress")
	}
	prog, ok := progV.(*OMap)
	if !ok {
		t.Fatalf("progress not map: %#v", progV)
	}
	if prog.Values()["enabled"] != true {
		t.Fatalf("enabled mismatch: %#v", prog.Values()["enabled"])
	}
}

func TestLocalExampleProgressHasChildren(t *testing.T) {
	path := filepath.Join("..", "..", "config", "secrets", "local.example.yaml")
	origBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read local.example.yaml: %v", err)
	}
	doc, err := parseYAML(origBytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfgV, _ := doc.root.Get("config")
	cfg := cfgV.(*OMap)
	feishuV, _ := cfg.Get("feishu")
	feishu := feishuV.(*OMap)
	asstV, _ := feishu.Get("assistant")
	asst := asstV.(*OMap)
	progV, ok := asst.Get("progress")
	if !ok {
		t.Fatalf("missing progress")
	}
	prog := progV.(*OMap)
	if _, ok := prog.Get("enabled"); !ok {
		paths := []string{}
		findKey(doc.root, "enabled", "", &paths)
		t.Fatalf("progress missing enabled; keys=%v all_enabled_paths=%v", prog.Keys(), paths)
	}
}

func TestListParsesAndStringifies(t *testing.T) {
	src := strings.Join([]string{
		"root:",
		"  add_dirs:",
		"    - \"/a\"",
		"    - \"/b\"",
		"",
	}, "\n")
	doc, err := parseYAML([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := normalizeText(doc.stringify())
	if normalizeText(src) != out {
		t.Fatalf("round-trip mismatch\n--- expected ---\n%s\n--- got ---\n%s", normalizeText(src), out)
	}
}

func TestUpsertPreservesExistingOrder(t *testing.T) {
	tmp := t.TempDir()
	secretsPath := filepath.Join(tmp, "local.yaml")
	body := strings.Join([]string{
		"config:",
		"  feishu:",
		"    assistant:",
		"      app_id: \"a\"",
		"      app_secret: \"b\"",
		"",
	}, "\n")
	if err := os.WriteFile(secretsPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("SUNCODEXCLAW_SECRETS_FILE", secretsPath)

	store := NewStore(tmp)
	_, err := store.UpsertSecretsEntry("feishu", "assistant", map[string]any{
		"verification_token": "v",
	})
	if err != nil {
		t.Fatalf("UpsertSecretsEntry: %v", err)
	}
	afterBytes, err := os.ReadFile(secretsPath)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	after := normalizeText(string(afterBytes))
	lines := strings.Split(after, "\n")
	// Ensure app_id appears before app_secret, and new key is appended after existing keys.
	idx := map[string]int{}
	for i, ln := range lines {
		if strings.Contains(ln, "app_id:") {
			idx["app_id"] = i
		}
		if strings.Contains(ln, "app_secret:") {
			idx["app_secret"] = i
		}
		if strings.Contains(ln, "verification_token:") {
			idx["verification_token"] = i
		}
	}
	if !(idx["app_id"] < idx["app_secret"] && idx["app_secret"] < idx["verification_token"]) {
		t.Fatalf("unexpected key order: %v\n%s", idx, after)
	}
}

func findKey(m *OMap, key string, prefix string, out *[]string) {
	if m == nil {
		return
	}
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		if k == key {
			*out = append(*out, prefix+"."+k)
		}
		if mm, ok := v.(*OMap); ok {
			findKey(mm, key, p, out)
		}
	}
}

func normalizeText(s string) string {
	// Normalize EOL and trailing whitespace for stable comparisons.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	out := strings.Join(lines, "\n")
	out = strings.TrimRight(out, "\n") + "\n"
	return out
}
