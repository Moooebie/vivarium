package keyring

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVaultLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	v := NewVault(path)
	if v.Exists() {
		t.Fatal("vault should not exist yet")
	}
	if err := v.StoreSecret("k1", "sk-1"); !errors.Is(err, ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	if err := v.Setup("hunter2"); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !v.IsUnlocked() {
		t.Fatal("vault should be unlocked after setup")
	}
	if err := v.Setup("again"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
	if err := v.StoreSecret("k1", "sk-1"); err != nil {
		t.Fatal(err)
	}
	got, err := v.GetSecret("k1")
	if err != nil || got != "sk-1" {
		t.Fatalf("GetSecret = %q, %v", got, err)
	}

	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if v.IsUnlocked() {
		t.Fatal("vault should be locked after Close")
	}

	reopened := NewVault(path)
	if err := reopened.Unlock("wrong"); err == nil {
		t.Fatal("expected wrong password error")
	}
	if err := reopened.Unlock("hunter2"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	got, err = reopened.GetSecret("k1")
	if err != nil || got != "sk-1" {
		t.Fatalf("reopened GetSecret = %q, %v", got, err)
	}
	if err := reopened.DeleteSecret("k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.GetSecret("k1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestVaultFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	v := NewVault(path)
	if err := v.Setup("pw"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("vault perm = %o, want 600", perm)
	}
}

func TestVaultTamperDetection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	v := NewVault(path)
	if err := v.Setup("pw"); err != nil {
		t.Fatal(err)
	}
	if err := v.StoreSecret("k1", "sk-1"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xFF
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	reopened := NewVault(path)
	if err := reopened.Unlock("pw"); err == nil {
		t.Fatal("expected tamper detection failure")
	}
}

func TestVaultReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	v := NewVault(path)
	if err := v.Setup("pw"); err != nil {
		t.Fatal(err)
	}
	if err := v.Reset(); err != nil {
		t.Fatal(err)
	}
	if v.Exists() {
		t.Fatal("vault file should be gone after reset")
	}
	if v.IsUnlocked() {
		t.Fatal("vault should be locked after reset")
	}
}
