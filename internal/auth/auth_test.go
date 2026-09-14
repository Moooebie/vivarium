package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"vivarium/internal/keyring"
	"vivarium/internal/models"
	"vivarium/internal/store"
)

func newManager(t *testing.T) *Manager {
	t.Helper()
	st := store.New(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	return NewManager(st, filepath.Join(st.Dir(), "vault.enc"))
}

func TestVaultAuthLifecycle(t *testing.T) {
	m := newManager(t)

	if s, err := m.Status(); err != nil || s != StatusUnconfigured {
		t.Fatalf("Status = %q, %v; want unconfigured", s, err)
	}
	if err := m.Setup(models.SecretVault, ""); err == nil {
		t.Fatal("expected empty password rejection")
	}
	if err := m.Setup(models.SecretVault, "pw"); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if s, _ := m.Status(); s != StatusUnlocked {
		t.Fatalf("Status = %q, want unlocked", s)
	}
	if err := m.Setup(models.SecretVault, "pw"); !errors.Is(err, keyring.ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}

	secrets, err := m.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.StoreSecret("k1", "sk-1"); err != nil {
		t.Fatal(err)
	}

	if err := m.vault.Lock(); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.Status(); s != StatusLocked {
		t.Fatalf("Status = %q, want locked", s)
	}
	if _, err := m.Secrets(); !errors.Is(err, keyring.ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}
	if err := m.Unlock("nope"); err == nil {
		t.Fatal("expected unlock failure")
	}
	if err := m.Unlock("pw"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if s, _ := m.Status(); s != StatusUnlocked {
		t.Fatalf("Status = %q, want unlocked", s)
	}

	if err := m.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if s, _ := m.Status(); s != StatusUnconfigured {
		t.Fatalf("Status after reset = %q, want unconfigured", s)
	}
}

func TestSetupUnknownBackend(t *testing.T) {
	m := newManager(t)
	if err := m.Setup("mystery", "pw"); err == nil {
		t.Fatal("expected unknown backend error")
	}
}

func TestLock(t *testing.T) {
	m := newManager(t)
	if err := m.Setup(models.SecretVault, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := m.Lock(); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.Status(); s != StatusLocked {
		t.Fatalf("Status = %q, want locked", s)
	}
}

func TestAutoLock(t *testing.T) {
	m := newManager(t)
	if err := m.Setup(models.SecretVault, "pw"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Secrets(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		m.AutoLock(ctx, 150*time.Millisecond)
		close(done)
	}()

	// Activity should keep the vault unlocked past the timeout.
	time.Sleep(80 * time.Millisecond)
	if _, err := m.Secrets(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if s, _ := m.Status(); s != StatusUnlocked {
		t.Fatalf("Status = %q, want unlocked while active", s)
	}

	// After going idle it must lock.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s, _ := m.Status(); s == StatusLocked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s, _ := m.Status(); s != StatusLocked {
		t.Fatalf("Status = %q, want locked after idle", s)
	}
	cancel()
	<-done
}
