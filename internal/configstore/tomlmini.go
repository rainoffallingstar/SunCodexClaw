package configstore

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// This is a deliberately small TOML subset parser/writer for bot overlay config:
// - tables: [shared], [shared.xxx], [bot.<account>], [bot.<account>.xxx]
// - scalars: string (single/double quoted or bare), bool, int
// - arrays of scalars
// It is intentionally conservative and tailored to the config files used by this repo.

type tomlDoc struct {
	root *OMap
}

func parseTOMLFile(path string) (*tomlDoc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseTOML(b)
}

func parseTOML(b []byte) (*tomlDoc, error) {
	root := NewOMap()
	current := []string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(stripTOMLComment(sc.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("tomlmini: unterminated table header (line %d)", lineNo)
			}
			parts, err := parseTOMLPath(line[1:len(line)-1], lineNo)
			if err != nil {
				return nil, err
			}
			ensureMapPath(root, parts)
			current = parts
			continue
		}
		eq := findTOMLSeparator(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("tomlmini: invalid assignment (line %d)", lineNo)
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			return nil, fmt.Errorf("tomlmini: empty key (line %d)", lineNo)
		}
		val, err := parseTOMLValue(strings.TrimSpace(line[eq+1:]), lineNo)
		if err != nil {
			return nil, err
		}
		entry := ensureMapPath(root, current)
		entry.Set(key, val)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return &tomlDoc{root: root}, nil
}

func stripTOMLComment(line string) string {
	var b strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && inDouble:
			b.WriteRune(r)
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			b.WriteRune(r)
		case r == '"' && !inSingle:
			inDouble = !inDouble
			b.WriteRune(r)
		case r == '#' && !inSingle && !inDouble:
			return b.String()
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func findTOMLSeparator(line string, target rune) int {
	inSingle := false
	inDouble := false
	escaped := false
	depth := 0
	for idx, r := range line {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inDouble:
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case inSingle || inDouble:
		case r == '[':
			depth++
		case r == ']' && depth > 0:
			depth--
		case depth == 0 && r == target:
			return idx
		}
	}
	return -1
}

func parseTOMLPath(raw string, lineNo int) ([]string, error) {
	inner := strings.TrimSpace(raw)
	if inner == "" {
		return nil, fmt.Errorf("tomlmini: empty table header (line %d)", lineNo)
	}
	parts := []string{}
	var b strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	for _, r := range inner {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && inDouble:
			b.WriteRune(r)
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			b.WriteRune(r)
		case r == '"' && !inSingle:
			inDouble = !inDouble
			b.WriteRune(r)
		case r == '.' && !inSingle && !inDouble:
			part, err := parseTOMLPathPart(b.String(), lineNo)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("tomlmini: unterminated quoted table part (line %d)", lineNo)
	}
	part, err := parseTOMLPathPart(b.String(), lineNo)
	if err != nil {
		return nil, err
	}
	parts = append(parts, part)
	return parts, nil
}

func parseTOMLPathPart(raw string, lineNo int) (string, error) {
	part := strings.TrimSpace(raw)
	if part == "" {
		return "", fmt.Errorf("tomlmini: empty table path part (line %d)", lineNo)
	}
	if strings.HasPrefix(part, "\"") && strings.HasSuffix(part, "\"") {
		return unquoteDouble(part[1 : len(part)-1])
	}
	if strings.HasPrefix(part, "'") && strings.HasSuffix(part, "'") {
		return part[1 : len(part)-1], nil
	}
	return part, nil
}

func parseTOMLValue(raw string, lineNo int) (any, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
		return unquoteDouble(s[1 : len(s)-1])
	}
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") && len(s) >= 2 {
		return s[1 : len(s)-1], nil
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return parseTOMLArray(s[1:len(s)-1], lineNo)
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
	return s, nil
}

func parseTOMLArray(raw string, lineNo int) ([]any, error) {
	inner := strings.TrimSpace(raw)
	if inner == "" {
		return []any{}, nil
	}
	parts := []string{}
	start := 0
	for start < len(inner) {
		idx := findTOMLSeparator(inner[start:], ',')
		if idx < 0 {
			parts = append(parts, strings.TrimSpace(inner[start:]))
			break
		}
		parts = append(parts, strings.TrimSpace(inner[start:start+idx]))
		start += idx + 1
	}
	out := make([]any, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		v, err := parseTOMLValue(part, lineNo)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
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

func (d *tomlDoc) stringify() string {
	var buf bytes.Buffer
	writeTOMLMap(&buf, d.root, nil)
	if buf.Len() == 0 || buf.Bytes()[buf.Len()-1] != '\n' {
		buf.WriteByte('\n')
	}
	return buf.String()
}

func writeTOMLMap(buf *bytes.Buffer, m *OMap, prefix []string) {
	if m == nil {
		return
	}
	scalars := []string{}
	children := []string{}
	for _, key := range m.Keys() {
		v, _ := m.Get(key)
		if _, ok := v.(*OMap); ok {
			children = append(children, key)
			continue
		}
		scalars = append(scalars, key)
	}
	if len(prefix) > 0 && len(scalars) > 0 {
		buf.WriteString("[")
		buf.WriteString(formatTOMLPath(prefix))
		buf.WriteString("]\n")
		for _, key := range scalars {
			v, _ := m.Get(key)
			buf.WriteString(key)
			buf.WriteString(" = ")
			buf.WriteString(formatTOMLValue(v))
			buf.WriteByte('\n')
		}
		buf.WriteByte('\n')
	}
	for _, key := range children {
		v, _ := m.Get(key)
		writeTOMLMap(buf, v.(*OMap), append(append([]string{}, prefix...), key))
	}
}

func formatTOMLPath(parts []string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if isBareTOMLKey(part) {
			out = append(out, part)
			continue
		}
		out = append(out, quoteDouble(part))
	}
	return strings.Join(out, ".")
}

func FormatTOMLPath(parts ...string) string {
	return formatTOMLPath(parts)
}

func isBareTOMLKey(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func formatTOMLValue(v any) string {
	switch tv := v.(type) {
	case bool:
		if tv {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(tv)
	case []any:
		items := make([]string, 0, len(tv))
		for _, item := range tv {
			items = append(items, formatTOMLValue(item))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case []string:
		items := make([]string, 0, len(tv))
		for _, item := range tv {
			items = append(items, quoteDouble(item))
		}
		return "[" + strings.Join(items, ", ") + "]"
	default:
		return quoteDouble(fmt.Sprintf("%v", tv))
	}
}
