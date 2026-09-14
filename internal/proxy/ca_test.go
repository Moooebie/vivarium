package proxy

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func testCA(t *testing.T) *CA {
	t.Helper()
	ca, err := loadOrCreate(t.TempDir(), 2048)
	if err != nil {
		t.Fatalf("create ca: %v", err)
	}
	return ca
}

func TestCALoadOrCreate(t *testing.T) {
	dir := t.TempDir()
	ca, err := loadOrCreate(dir, 2048)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, CAKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("ca key perm = %o, want 600", perm)
	}
	reloaded, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(reloaded.CertPEM()) != string(ca.CertPEM()) {
		t.Fatal("reloaded CA differs from generated CA")
	}
}

func TestCALeafChain(t *testing.T) {
	ca := testCA(t)
	leaf, err := ca.Leaf("api.openai.com")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Subject.CommonName != "api.openai.com" {
		t.Fatalf("leaf CN = %q", parsed.Subject.CommonName)
	}
	intermediates := x509.NewCertPool()
	for _, der := range leaf.Certificate[1:] {
		c, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		intermediates.AddCert(c)
	}
	if _, err := parsed.Verify(x509.VerifyOptions{
		DNSName:       "api.openai.com",
		Roots:         ca.RootPool(),
		Intermediates: intermediates,
	}); err != nil {
		t.Fatalf("leaf does not verify against CA: %v", err)
	}

	// The leaf must be cached.
	again, err := ca.Leaf("api.openai.com")
	if err != nil {
		t.Fatal(err)
	}
	if again != leaf {
		t.Fatal("expected cached leaf")
	}
}

func TestCALeafForIP(t *testing.T) {
	ca := testCA(t)
	leaf, err := ca.Leaf("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.IPAddresses) != 1 || parsed.IPAddresses[0].String() != "127.0.0.1" {
		t.Fatalf("unexpected IP SANs: %v", parsed.IPAddresses)
	}
}
