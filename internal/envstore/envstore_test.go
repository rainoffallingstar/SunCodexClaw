package envstore

import (
	"errors"
	"os"
	"testing"
)

func TestStoreSetResolveListDelete(t *testing.T) {
	store := NewStore(t.TempDir())

	globalEntry, err := store.Set(ScopeGlobal, "", "API_TOKEN", "global-secret", "test/global")
	if err != nil {
		t.Fatalf("Set(global) error = %v", err)
	}
	if globalEntry.Scope != ScopeGlobal {
		t.Fatalf("global scope = %q", globalEntry.Scope)
	}

	accountEntry, err := store.Set(ScopeAccount, "assistant", "API_TOKEN", "account-secret", "test/account")
	if err != nil {
		t.Fatalf("Set(account) error = %v", err)
	}
	if accountEntry.Account != "assistant" {
		t.Fatalf("account entry account = %q", accountEntry.Account)
	}

	resolved, err := store.Resolve("assistant", "API_TOKEN")
	if err != nil {
		t.Fatalf("Resolve(account) error = %v", err)
	}
	if resolved.Value != "account-secret" {
		t.Fatalf("Resolve(account).Value = %q, want account-secret", resolved.Value)
	}

	resolvedGlobal, err := store.Resolve("other", "API_TOKEN")
	if err != nil {
		t.Fatalf("Resolve(global fallback) error = %v", err)
	}
	if resolvedGlobal.Value != "global-secret" {
		t.Fatalf("Resolve(global fallback).Value = %q, want global-secret", resolvedGlobal.Value)
	}

	allEntries, err := store.List(ScopeAll, "assistant")
	if err != nil {
		t.Fatalf("List(all) error = %v", err)
	}
	if len(allEntries) != 2 {
		t.Fatalf("List(all) len = %d, want 2", len(allEntries))
	}

	if err := store.Delete(ScopeAccount, "assistant", "API_TOKEN"); err != nil {
		t.Fatalf("Delete(account) error = %v", err)
	}
	afterDelete, err := store.Resolve("assistant", "API_TOKEN")
	if err != nil {
		t.Fatalf("Resolve(after delete) error = %v", err)
	}
	if afterDelete.Value != "global-secret" {
		t.Fatalf("Resolve(after delete).Value = %q, want global-secret", afterDelete.Value)
	}
}

func TestGetMissingReturnsNotExist(t *testing.T) {
	store := NewStore(t.TempDir())
	_, err := store.Get(ScopeGlobal, "", "MISSING")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Get(missing) error = %v, want os.ErrNotExist", err)
	}
}

func TestMaskedValueDoesNotLeakRawSecret(t *testing.T) {
	masked := MaskedValue("super-secret")
	if masked == "super-secret" {
		t.Fatalf("MaskedValue leaked raw secret")
	}
	if masked == "" {
		t.Fatalf("MaskedValue returned empty string")
	}
}

func TestValidateKeyRejectsInvalidNames(t *testing.T) {
	for _, key := range []string{"", "1TOKEN", "A-B", "A.B", "A B"} {
		if err := ValidateKey(key); err == nil {
			t.Fatalf("ValidateKey(%q) = nil, want error", key)
		}
	}
	if err := ValidateKey("OPENAI_API_KEY"); err != nil {
		t.Fatalf("ValidateKey(valid) error = %v", err)
	}
}
