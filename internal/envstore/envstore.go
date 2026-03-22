package envstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	ScopeGlobal  = "global"
	ScopeAccount = "account"
	ScopeAll     = "all"
	ScopeAuto    = "auto"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

type Entry struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Scope     string `json:"scope"`
	Account   string `json:"account,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type Store struct {
	RepoRoot string
}

func NewStore(repoRoot string) *Store {
	return &Store{RepoRoot: repoRoot}
}

func ValidateScope(scope string) error {
	switch strings.TrimSpace(scope) {
	case ScopeGlobal, ScopeAccount, ScopeAll, ScopeAuto:
		return nil
	default:
		return fmt.Errorf("invalid env scope %q", scope)
	}
}

func ValidateKey(key string) error {
	if !keyPattern.MatchString(strings.TrimSpace(key)) {
		return fmt.Errorf("invalid env key %q", key)
	}
	return nil
}

func (s *Store) Set(scope, account, key, value, updatedBy string) (Entry, error) {
	scope = normalizeScope(scope)
	if scope != ScopeGlobal && scope != ScopeAccount {
		return Entry{}, fmt.Errorf("env set requires scope global|account")
	}
	key = strings.TrimSpace(key)
	if err := ValidateKey(key); err != nil {
		return Entry{}, err
	}
	account = normalizedAccount(scope, account)
	if scope == ScopeAccount && account == "" {
		return Entry{}, fmt.Errorf("account scope requires account")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	entry := Entry{
		Key:       key,
		Value:     value,
		Scope:     scope,
		Account:   account,
		UpdatedBy: strings.TrimSpace(updatedBy),
		CreatedAt: now,
		UpdatedAt: now,
	}
	existing, err := s.Get(scope, account, key)
	if err == nil {
		entry.CreatedAt = firstNonEmpty(existing.CreatedAt, now)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Entry{}, err
	}
	if err := s.writeEntry(entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (s *Store) Get(scope, account, key string) (Entry, error) {
	scope = normalizeScope(scope)
	if scope != ScopeGlobal && scope != ScopeAccount {
		return Entry{}, fmt.Errorf("env get requires scope global|account")
	}
	key = strings.TrimSpace(key)
	if err := ValidateKey(key); err != nil {
		return Entry{}, err
	}
	account = normalizedAccount(scope, account)
	path, err := s.entryPath(scope, account, key)
	if err != nil {
		return Entry{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, err
	}
	var entry Entry
	if err := json.Unmarshal(body, &entry); err != nil {
		return Entry{}, err
	}
	if entry.Key == "" {
		entry.Key = key
	}
	if entry.Scope == "" {
		entry.Scope = scope
	}
	if entry.Scope == ScopeAccount && entry.Account == "" {
		entry.Account = account
	}
	return entry, nil
}

func (s *Store) Resolve(account, key string) (Entry, error) {
	account = strings.TrimSpace(account)
	if account != "" {
		if entry, err := s.Get(ScopeAccount, account, key); err == nil {
			return entry, nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Entry{}, err
		}
	}
	return s.Get(ScopeGlobal, "", key)
}

func (s *Store) Delete(scope, account, key string) error {
	scope = normalizeScope(scope)
	if scope != ScopeGlobal && scope != ScopeAccount {
		return fmt.Errorf("env delete requires scope global|account")
	}
	key = strings.TrimSpace(key)
	if err := ValidateKey(key); err != nil {
		return err
	}
	account = normalizedAccount(scope, account)
	path, err := s.entryPath(scope, account, key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) List(scope, account string) ([]Entry, error) {
	scope = normalizeScope(scope)
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	account = strings.TrimSpace(account)
	switch scope {
	case ScopeGlobal:
		return s.listDir(s.globalDir(), ScopeGlobal, "")
	case ScopeAccount:
		if account == "" {
			return nil, fmt.Errorf("account scope requires account")
		}
		return s.listDir(s.accountDir(account), ScopeAccount, account)
	case ScopeAll:
		out := []Entry{}
		globalEntries, err := s.listDir(s.globalDir(), ScopeGlobal, "")
		if err != nil {
			return nil, err
		}
		out = append(out, globalEntries...)
		if account != "" {
			accountEntries, err := s.listDir(s.accountDir(account), ScopeAccount, account)
			if err != nil {
				return nil, err
			}
			out = append(out, accountEntries...)
		}
		sortEntries(out)
		return out, nil
	default:
		return nil, fmt.Errorf("env list requires scope global|account|all")
	}
}

func MaskedValue(value string) string {
	if value == "" {
		return "(hidden empty)"
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("(hidden len=%d sha256=%s)", len(value), hex.EncodeToString(sum[:])[:12])
}

func SanitizeAccount(raw string) string {
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

func (s *Store) baseDir() string {
	return filepath.Join(s.RepoRoot, "config", "envdb")
}

func (s *Store) globalDir() string {
	return filepath.Join(s.baseDir(), "global")
}

func (s *Store) accountDir(account string) string {
	return filepath.Join(s.baseDir(), "accounts", SanitizeAccount(account))
}

func (s *Store) entryPath(scope, account, key string) (string, error) {
	switch scope {
	case ScopeGlobal:
		return filepath.Join(s.globalDir(), key+".json"), nil
	case ScopeAccount:
		account = strings.TrimSpace(account)
		if account == "" {
			return "", fmt.Errorf("account scope requires account")
		}
		return filepath.Join(s.accountDir(account), key+".json"), nil
	default:
		return "", fmt.Errorf("invalid env scope %q", scope)
	}
}

func (s *Store) writeEntry(entry Entry) error {
	path, err := s.entryPath(entry.Scope, entry.Account, entry.Key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}

func (s *Store) listDir(dir, scope, account string) ([]Entry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Entry{}, nil
		}
		return nil, err
	}
	out := make([]Entry, 0, len(ents))
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		key := strings.TrimSuffix(ent.Name(), ".json")
		entry, err := s.Get(scope, account, key)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	sortEntries(out)
	return out, nil
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Scope != entries[j].Scope {
			return entries[i].Scope < entries[j].Scope
		}
		if entries[i].Account != entries[j].Account {
			return entries[i].Account < entries[j].Account
		}
		return entries[i].Key < entries[j].Key
	})
}

func normalizeScope(scope string) string {
	scope = strings.TrimSpace(strings.ToLower(scope))
	if scope == "" {
		return ScopeAuto
	}
	return scope
}

func normalizedAccount(scope, account string) string {
	if scope != ScopeAccount {
		return ""
	}
	return strings.TrimSpace(account)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
