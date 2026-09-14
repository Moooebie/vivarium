package main

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"vivarium/internal/auth"
	"vivarium/internal/models"
	"vivarium/internal/store"
)

func newAuditEnv(t *testing.T) (*store.Store, *auth.Manager) {
	t.Helper()
	dir := t.TempDir()
	st := store.New(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	am := auth.NewManager(st, filepath.Join(dir, "vault.enc"))
	if err := am.Setup(models.SecretVault, "pw"); err != nil {
		t.Fatal(err)
	}
	return st, am
}

func captureLog(fn func()) string {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	fn()
	return buf.String()
}

func TestAuditSecretsWarnsOnMissing(t *testing.T) {
	st, am := newAuditEnv(t)
	key := models.APIKey{
		ID: "k1", Name: "NoSecret", ProviderType: models.ProviderOpenAI,
		BaseURL: "https://x/v1", MockURL: "https://x/v1", CreatedAt: 1,
	}
	if err := st.PutAPIKey(key); err != nil {
		t.Fatal(err)
	}

	out := captureLog(func() { auditSecrets(st, am) })
	if !strings.Contains(out, "NoSecret") {
		t.Fatalf("audit log = %q, want a warning naming the key", out)
	}
}

func TestAuditSecretsQuietWhenPresent(t *testing.T) {
	st, am := newAuditEnv(t)
	secrets, err := am.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.StoreSecret("k1", "sk"); err != nil {
		t.Fatal(err)
	}
	key := models.APIKey{
		ID: "k1", Name: "WithSecret", ProviderType: models.ProviderOpenAI,
		BaseURL: "https://x/v1", MockURL: "https://x/v1", CreatedAt: 1,
	}
	if err := st.PutAPIKey(key); err != nil {
		t.Fatal(err)
	}

	if out := captureLog(func() { auditSecrets(st, am) }); out != "" {
		t.Fatalf("audit log = %q, want no warning", out)
	}
}
