package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"vivarium/internal/models"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := New(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return s
}

func TestInitSeedsPresets(t *testing.T) {
	s := newTestStore(t)
	imgs, err := s.ListBaseImages()
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 2 {
		t.Fatalf("expected 2 preset images, got %d", len(imgs))
	}
	cfg, err := s.Config()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Configured {
		t.Fatalf("fresh install should be unconfigured, got %+v", cfg)
	}
}

func TestInitIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	// Add a user image, then re-init; it must survive.
	img := models.BaseImage{ID: "u1", Name: "Custom", SourceType: models.SourceDockerHub, SourcePathOrRepo: "x/y", DefaultUser: "root"}
	if err := s.PutBaseImage(img); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBaseImage("u1")
	if err != nil {
		t.Fatalf("user image lost on re-init: %v", err)
	}
	if got.Name != "Custom" {
		t.Fatalf("unexpected image: %+v", got)
	}
}

func TestBaseImageDeleteInternalForbidden(t *testing.T) {
	s := newTestStore(t)
	err := s.DeleteBaseImage(StandardOpenCodeImageID)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict deleting internal image, got %v", err)
	}
}

func TestCRUDRoundTrip(t *testing.T) {
	s := newTestStore(t)
	key := models.APIKey{ID: "k1", Name: "K", ProviderType: models.ProviderOpenAI,
		BaseURL: "https://api.openai.com/v1", MockURL: "https://api.openai.com/v1", CreatedAt: 1}
	if err := s.PutAPIKey(key); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAPIKey("k1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "K" {
		t.Fatalf("unexpected key: %+v", got)
	}
	if err := s.DeleteAPIKey("k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAPIKey("k1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestFilesAreRestricted(t *testing.T) {
	s := newTestStore(t)
	info, err := os.Stat(filepath.Join(s.Dir(), "base_images.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file perm = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir perm = %o, want 700", perm)
	}
}
