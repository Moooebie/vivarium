// Package store implements atomic, permission-restricted JSON persistence for
// the Vivarium entities. Data lives in $XDG_DATA_HOME/vivarium.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"vivarium/internal/models"
)

// ErrNotFound is returned when an entity does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a uniqueness constraint is violated.
var ErrConflict = errors.New("conflict")

const (
	filePerm = 0o600
	dirPerm  = 0o700
)

// Store provides typed access to the JSON metadata files. It is safe for
// concurrent use; every mutation rewrites the whole file atomically.
type Store struct {
	dir string
	mu  sync.Mutex
}

// New returns a Store rooted at dir. Call Init before use.
func New(dir string) *Store { return &Store{dir: dir} }

// Dir returns the storage directory.
func (s *Store) Dir() string { return s.dir }

// Init creates the storage directory and seeds defaults on first run.
func (s *Store) Init() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, dirPerm); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if err := os.Chmod(s.dir, dirPerm); err != nil {
		return fmt.Errorf("restrict data dir: %w", err)
	}
	if err := s.ensureFile(s.baseImagesPath(), StandardBaseImages()); err != nil {
		return err
	}
	for _, p := range []string{s.apiKeysPath(), s.recipesPath(), s.instancesPath()} {
		if err := s.ensureFile(p, []any{}); err != nil {
			return err
		}
	}
	return s.ensureFile(s.configPath(), models.Config{})
}

func (s *Store) ensureFile(path string, def any) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeJSON(path, def)
}

func (s *Store) baseImagesPath() string { return filepath.Join(s.dir, "base_images.json") }
func (s *Store) apiKeysPath() string    { return filepath.Join(s.dir, "api_keys.json") }
func (s *Store) recipesPath() string    { return filepath.Join(s.dir, "recipes.json") }
func (s *Store) instancesPath() string  { return filepath.Join(s.dir, "instances.json") }
func (s *Store) configPath() string     { return filepath.Join(s.dir, "config.json") }

// ---- Base Images ----

// ListBaseImages returns all registered base images.
func (s *Store) ListBaseImages() ([]models.BaseImage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readJSON[[]models.BaseImage](s.baseImagesPath())
}

// GetBaseImage returns a base image by ID.
func (s *Store) GetBaseImage(id string) (models.BaseImage, error) {
	all, err := s.ListBaseImages()
	if err != nil {
		return models.BaseImage{}, err
	}
	for _, b := range all {
		if b.ID == id {
			return b, nil
		}
	}
	return models.BaseImage{}, fmt.Errorf("base image %q: %w", id, ErrNotFound)
}

// PutBaseImage inserts or updates a base image.
func (s *Store) PutBaseImage(b models.BaseImage) error {
	if err := b.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := readJSON[[]models.BaseImage](s.baseImagesPath())
	if err != nil {
		return err
	}
	replaced := false
	for i := range all {
		if all[i].ID == b.ID {
			all[i] = b
			replaced = true
			break
		}
	}
	if !replaced {
		all = append(all, b)
	}
	return writeJSON(s.baseImagesPath(), all)
}

// DeleteBaseImage removes a base image. Internal images cannot be removed.
func (s *Store) DeleteBaseImage(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := readJSON[[]models.BaseImage](s.baseImagesPath())
	if err != nil {
		return err
	}
	out := all[:0]
	found := false
	for _, b := range all {
		if b.ID != id {
			out = append(out, b)
			continue
		}
		if b.IsInternal {
			return fmt.Errorf("base image %q is a standard preset: %w", id, ErrConflict)
		}
		found = true
	}
	if !found {
		return fmt.Errorf("base image %q: %w", id, ErrNotFound)
	}
	return writeJSON(s.baseImagesPath(), out)
}

// ---- API Keys ----

// ListAPIKeys returns all API key metadata records.
func (s *Store) ListAPIKeys() ([]models.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readJSON[[]models.APIKey](s.apiKeysPath())
}

// GetAPIKey returns API key metadata by ID.
func (s *Store) GetAPIKey(id string) (models.APIKey, error) {
	all, err := s.ListAPIKeys()
	if err != nil {
		return models.APIKey{}, err
	}
	for _, k := range all {
		if k.ID == id {
			return k, nil
		}
	}
	return models.APIKey{}, fmt.Errorf("api key %q: %w", id, ErrNotFound)
}

// PutAPIKey inserts or updates API key metadata.
func (s *Store) PutAPIKey(k models.APIKey) error {
	if err := k.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := readJSON[[]models.APIKey](s.apiKeysPath())
	if err != nil {
		return err
	}
	replaced := false
	for i := range all {
		if all[i].ID == k.ID {
			all[i] = k
			replaced = true
			break
		}
	}
	if !replaced {
		all = append(all, k)
	}
	return writeJSON(s.apiKeysPath(), all)
}

// DeleteAPIKey removes API key metadata by ID.
func (s *Store) DeleteAPIKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := readJSON[[]models.APIKey](s.apiKeysPath())
	if err != nil {
		return err
	}
	out, found := filter(all, func(k models.APIKey) bool { return k.ID != id })
	if !found {
		return fmt.Errorf("api key %q: %w", id, ErrNotFound)
	}
	return writeJSON(s.apiKeysPath(), out)
}

// ClearAPIKeys removes all API key metadata records.
func (s *Store) ClearAPIKeys() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(s.apiKeysPath(), []models.APIKey{})
}

// ---- Recipes ----

// ListRecipes returns all saved recipes.
func (s *Store) ListRecipes() ([]models.Recipe, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readJSON[[]models.Recipe](s.recipesPath())
}

// GetRecipe returns a recipe by ID.
func (s *Store) GetRecipe(id string) (models.Recipe, error) {
	all, err := s.ListRecipes()
	if err != nil {
		return models.Recipe{}, err
	}
	for _, r := range all {
		if r.ID == id {
			return r, nil
		}
	}
	return models.Recipe{}, fmt.Errorf("recipe %q: %w", id, ErrNotFound)
}

// PutRecipe inserts or updates a recipe.
func (s *Store) PutRecipe(r models.Recipe) error {
	if err := r.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := readJSON[[]models.Recipe](s.recipesPath())
	if err != nil {
		return err
	}
	replaced := false
	for i := range all {
		if all[i].ID == r.ID {
			all[i] = r
			replaced = true
			break
		}
	}
	if !replaced {
		all = append(all, r)
	}
	return writeJSON(s.recipesPath(), all)
}

// DeleteRecipe removes a recipe by ID.
func (s *Store) DeleteRecipe(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := readJSON[[]models.Recipe](s.recipesPath())
	if err != nil {
		return err
	}
	out, found := filter(all, func(r models.Recipe) bool { return r.ID != id })
	if !found {
		return fmt.Errorf("recipe %q: %w", id, ErrNotFound)
	}
	return writeJSON(s.recipesPath(), out)
}

// ---- Instances ----

// ListInstances returns all managed instances.
func (s *Store) ListInstances() ([]models.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readJSON[[]models.Instance](s.instancesPath())
}

// GetInstance returns an instance by ID.
func (s *Store) GetInstance(id string) (models.Instance, error) {
	all, err := s.ListInstances()
	if err != nil {
		return models.Instance{}, err
	}
	for _, i := range all {
		if i.ID == id {
			return i, nil
		}
	}
	return models.Instance{}, fmt.Errorf("instance %q: %w", id, ErrNotFound)
}

// PutInstance inserts or updates an instance.
func (s *Store) PutInstance(i models.Instance) error {
	if err := i.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := readJSON[[]models.Instance](s.instancesPath())
	if err != nil {
		return err
	}
	replaced := false
	for idx := range all {
		if all[idx].ID == i.ID {
			all[idx] = i
			replaced = true
			break
		}
	}
	if !replaced {
		all = append(all, i)
	}
	return writeJSON(s.instancesPath(), all)
}

// DeleteInstance removes an instance record by ID.
func (s *Store) DeleteInstance(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := readJSON[[]models.Instance](s.instancesPath())
	if err != nil {
		return err
	}
	out, found := filter(all, func(i models.Instance) bool { return i.ID != id })
	if !found {
		return fmt.Errorf("instance %q: %w", id, ErrNotFound)
	}
	return writeJSON(s.instancesPath(), out)
}

// ---- Config ----

// Config returns the persisted configuration, defaulting when absent.
func (s *Store) Config() (models.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readJSON[models.Config](s.configPath())
}

// SaveConfig persists the configuration.
func (s *Store) SaveConfig(c models.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(s.configPath(), c)
}

// filter returns a copy of items for which keep returns true, and whether any
// item was dropped.
func filter[T any](items []T, keep func(T) bool) ([]T, bool) {
	out := make([]T, 0, len(items))
	dropped := false
	for _, it := range items {
		if keep(it) {
			out = append(out, it)
		} else {
			dropped = true
		}
	}
	return out, dropped
}

func readJSON[T any](path string) (T, error) {
	var v T
	data, err := os.ReadFile(path)
	if err != nil {
		return v, err
	}
	if len(data) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return v, nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, filePerm)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
