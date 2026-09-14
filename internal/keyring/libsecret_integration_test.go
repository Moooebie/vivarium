//go:build integration

package keyring

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestLibSecretIntegration exercises the real FreeDesktop Secret Service.
// Enable with: VIVARIUM_INTEGRATION=1 go test -tags=integration ./internal/keyring/
func TestLibSecretIntegration(t *testing.T) {
	if os.Getenv("VIVARIUM_INTEGRATION") == "" {
		t.Skip("set VIVARIUM_INTEGRATION=1 to run Secret Service integration tests")
	}
	s := NewLibSecret()
	if !s.Available() {
		t.Skip("no Secret Service available")
	}
	defer s.Close()

	collection, err := s.CollectionPath()
	if err != nil {
		t.Fatalf("resolve collection: %v", err)
	}
	if strings.Contains(collection, "session") {
		t.Fatalf("resolved the ephemeral session collection: %s", collection)
	}
	t.Logf("using keyring collection %s", collection)

	const id = "vivarium-integration-test-key"
	if err := s.StoreSecret(id, "sk-integration"); err != nil {
		t.Fatalf("StoreSecret: %v", err)
	}
	if got, err := s.GetSecret(id); err != nil || got != "sk-integration" {
		t.Fatalf("GetSecret = %q, %v", got, err)
	}
	if err := s.StoreSecret(id, "sk-updated"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got, _ := s.GetSecret(id); got != "sk-updated" {
		t.Fatalf("updated value = %q", got)
	}
	if err := s.DeleteSecret(id); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}
	if _, err := s.GetSecret(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
