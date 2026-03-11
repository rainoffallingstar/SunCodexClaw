package configstore

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// This is a deliberately small YAML subset parser/writer:
// - mappings (key: value, key: <newline>)
// - sequences (- value)
// - scalars: string (quoted/unquoted), bool, int
// - 2-space indents, no tabs
// It matches the local.yaml templates used by this repo and preserves key order.

type yamlDoc struct {
	root *OMap
}

func parseYAMLFile(path string) (*yamlDoc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseYAML(b)
}

func parseYAML(b []byte) (*yamlDoc, error) {
	root := NewOMap()
	type frame struct {
		indent     int
		kind       string // "map" or "list"
		m          *OMap
		l          *[]any
		pendingKey string // last key created with no value (may become map or list)
		pendingInd int    // indentation level of pendingKey line
	}
	stack := []frame{{indent: -1, kind: "map", m: root}}

	sc := bufio.NewScanner(bytes.NewReader(b))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		trimRight := strings.TrimRight(raw, " \t")
		line := trimRight
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, "\t") {
			return nil, fmt.Errorf("yamlmini: tabs not supported (line %d)", lineNo)
		}
		indent := countLeadingSpaces(line)
		if indent%2 != 0 {
			return nil, fmt.Errorf("yamlmini: indentation must be multiple of 2 (line %d)", lineNo)
		}
		content := strings.TrimLeft(line, " ")

		for len(stack) > 0 && indent <= stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}
		parentIdx := len(stack) - 1
		parent := &stack[parentIdx]

		// If we have a pending key but the next non-empty line is not a child (same or less indent),
		// treat it as an empty map and clear pending state.
		if parent.kind == "map" && parent.pendingKey != "" && indent <= parent.pendingInd {
			child := NewOMap()
			parent.m.Set(parent.pendingKey, child)
			parent.pendingKey = ""
			parent.pendingInd = 0
		}

		// If parent is a map and has a pendingKey, decide whether it should be a list or map,
		// based on the first child line under that key.
		if parent.kind == "map" && parent.pendingKey != "" && indent > parent.pendingInd {
			pk := parent.pendingKey
			parent.pendingKey = ""
			parent.pendingInd = 0

			if strings.HasPrefix(content, "- ") {
				lst := []any{}
				parent.m.Set(pk, &lst)
				// list items are indented 2 spaces under the key line
				stack = append(stack, frame{indent: indent - 2, kind: "list", l: &lst})
			} else {
				child := NewOMap()
				parent.m.Set(pk, child)
				// child mappings are indented 2 spaces under the key line
				stack = append(stack, frame{indent: indent - 2, kind: "map", m: child})
			}
			parentIdx = len(stack) - 1
			parent = &stack[parentIdx]
		}

		if strings.HasPrefix(content, "- ") {
			if parent.kind != "list" {
				return nil, fmt.Errorf("yamlmini: list item without list context (line %d)", lineNo)
			}
			itemRaw := strings.TrimSpace(strings.TrimPrefix(content, "- "))
			itemVal, err := parseScalar(itemRaw)
			if err != nil {
				return nil, fmt.Errorf("yamlmini: %w (line %d)", err, lineNo)
			}
			*parent.l = append(*parent.l, itemVal)
			continue
		}

		// key: value or key:
		colon := strings.Index(content, ":")
		if colon < 0 {
			return nil, fmt.Errorf("yamlmini: expected mapping entry (line %d)", lineNo)
		}
		key := strings.TrimSpace(content[:colon])
		rest := strings.TrimSpace(content[colon+1:])
		if key == "" {
			return nil, fmt.Errorf("yamlmini: empty key (line %d)", lineNo)
		}
		if parent.kind != "map" {
			return nil, fmt.Errorf("yamlmini: mapping entry under list is unsupported (line %d)", lineNo)
		}

		if rest == "" {
			// Create lazily when we see the first child line (could be a list or map).
			parent.pendingKey = key
			parent.pendingInd = indent
			continue
		}
		val, err := parseScalar(rest)
		if err != nil {
			return nil, fmt.Errorf("yamlmini: %w (line %d)", err, lineNo)
		}
		parent.m.Set(key, val)
		parent.pendingKey = ""
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return &yamlDoc{root: root}, nil
}

func countLeadingSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

func parseScalar(raw string) (any, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if s == "true" {
		return true, nil
	}
	if s == "false" {
		return false, nil
	}
	if i, err := strconv.Atoi(s); err == nil {
		return i, nil
	}
	// quoted string
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
		u, err := unquoteDouble(s[1 : len(s)-1])
		if err != nil {
			return nil, err
		}
		return u, nil
	}
	// bare string
	return s, nil
}

func unquoteDouble(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch != '\\' {
			b.WriteByte(ch)
			continue
		}
		if i+1 >= len(s) {
			return "", fmt.Errorf("invalid escape")
		}
		i++
		switch s[i] {
		case '\\', '"':
			b.WriteByte(s[i])
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		default:
			// keep unknown escapes verbatim
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

func quoteDouble(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (d *yamlDoc) stringify() string {
	var buf bytes.Buffer
	writeOMap(&buf, d.root, 0)
	if buf.Len() == 0 || buf.Bytes()[buf.Len()-1] != '\n' {
		buf.WriteByte('\n')
	}
	return buf.String()
}

func writeOMap(w *bytes.Buffer, m *OMap, indent int) {
	if m == nil {
		return
	}
	prefix := strings.Repeat(" ", indent)
	for _, k := range m.keys {
		v := m.values[k]
		switch tv := v.(type) {
		case *OMap:
			fmt.Fprintf(w, "%s%s:\n", prefix, k)
			writeOMap(w, tv, indent+2)
		case *[]any:
			fmt.Fprintf(w, "%s%s:\n", prefix, k)
			writeList(w, *tv, indent+2)
		case []any:
			fmt.Fprintf(w, "%s%s:\n", prefix, k)
			writeList(w, tv, indent+2)
		case bool:
			if tv {
				fmt.Fprintf(w, "%s%s: true\n", prefix, k)
			} else {
				fmt.Fprintf(w, "%s%s: false\n", prefix, k)
			}
		case int:
			fmt.Fprintf(w, "%s%s: %d\n", prefix, k, tv)
		case string:
			fmt.Fprintf(w, "%s%s: %s\n", prefix, k, quoteDouble(tv))
		default:
			// fall back to string
			fmt.Fprintf(w, "%s%s: %s\n", prefix, k, quoteDouble(fmt.Sprintf("%v", tv)))
		}
	}
}

func writeList(w *bytes.Buffer, items []any, indent int) {
	prefix := strings.Repeat(" ", indent)
	for _, it := range items {
		switch tv := it.(type) {
		case bool:
			if tv {
				fmt.Fprintf(w, "%s- true\n", prefix)
			} else {
				fmt.Fprintf(w, "%s- false\n", prefix)
			}
		case int:
			fmt.Fprintf(w, "%s- %d\n", prefix, tv)
		case string:
			fmt.Fprintf(w, "%s- %s\n", prefix, quoteDouble(tv))
		default:
			fmt.Fprintf(w, "%s- %s\n", prefix, quoteDouble(fmt.Sprintf("%v", tv)))
		}
	}
}

func resolveSecretsFile(repoRoot string) string {
	explicit := strings.TrimSpace(
		firstNonEmpty(
			os.Getenv("SUNCODEXCLAW_SECRET_YAML"),
			os.Getenv("SUNCODEXCLAW_SECRETS_FILE"),
			os.Getenv("CODEX_CLAW_SECRET_YAML"),
			os.Getenv("CODEX_CLAW_SECRETS_FILE"),
		),
	)
	if explicit != "" {
		if filepath.IsAbs(explicit) {
			return explicit
		}
		return filepath.Join(repoRoot, explicit)
	}
	return filepath.Join(repoRoot, "config", "secrets", "local.yaml")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
