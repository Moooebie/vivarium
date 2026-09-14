// Package auth manages the encryption setup lifecycle: backend selection,
// vault unlock, status reporting, and credential reset.
package auth

import (
	"errors"
	"fmt"
	"sync"

	"vivarium/internal/keyring"
	"vivarium/internal/models"
	"vivarium/internal/store"
)

// ErrUnconfigured indicates encryption setup has not run yet.
var ErrUnconfigured = errors.New("credentials are not configured")

// Status describes the current credential-store state.
type Status string

const (
	// StatusUnconfigured means encryption setup has not run yet.
	StatusUnconfigured Status = "unconfigured"
	// StatusLocked means the vault exists but has not been unlocked.
	StatusLocked Status = "locked"
	// StatusUnlocked means secrets are accessible.
	StatusUnlocked Status = "unlocked"
)

// Manager owns the secret backend selection and unlock state.
type Manager struct {
	store     *store.Store
	vault     *keyring.Vault
	libsecret *keyring.LibSecret
	mu        sync.Mutex
}

// NewManager builds a Manager rooted at the given store and vault path.
func NewManager(st *store.Store, vaultPath string) *Manager {
	return &Manager{
		store:     st,
		vault:     keyring.NewVault(vaultPath),
		libsecret: keyring.NewLibSecret(),
	}
}

// Vault exposes the underlying vault (used for status/diagnostics).
func (m *Manager) Vault() *keyring.Vault { return m.vault }

// Status returns the current credential-store state.
func (m *Manager) Status() (Status, error) {
	cfg, err := m.store.Config()
	if err != nil {
		return "", err
	}
	if !cfg.Configured {
		return StatusUnconfigured, nil
	}
	switch cfg.SecretStorageType {
	case models.SecretVault:
		if m.vault.IsUnlocked() {
			return StatusUnlocked, nil
		}
		return StatusLocked, nil
	case models.SecretLibsecret:
		if m.libsecret.Available() {
			return StatusUnlocked, nil
		}
		return StatusLocked, nil
	default:
		return StatusUnconfigured, nil
	}
}

// Setup performs first-run configuration for the chosen backend. For the vault
// backend password is required; for libsecret it is ignored.
func (m *Manager) Setup(storage models.SecretStorageType, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, err := m.store.Config()
	if err != nil {
		return err
	}
	if cfg.Configured {
		return keyring.ErrAlreadyExists
	}
	switch storage {
	case models.SecretVault:
		if err := m.vault.Setup(password); err != nil {
			return err
		}
	case models.SecretLibsecret:
		if !m.libsecret.Available() {
			return keyring.ErrUnavailable
		}
	default:
		return fmt.Errorf("unknown secret storage type %q", storage)
	}
	return m.store.SaveConfig(models.Config{SecretStorageType: storage, Configured: true})
}

// Unlock unlocks the vault backend. It is a no-op for libsecret.
func (m *Manager) Unlock(password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, err := m.store.Config()
	if err != nil {
		return err
	}
	if !cfg.Configured {
		return ErrUnconfigured
	}
	if cfg.SecretStorageType == models.SecretVault {
		return m.vault.Unlock(password)
	}
	return nil
}

// Secrets returns the active secret store, or ErrLocked when unavailable.
func (m *Manager) Secrets() (keyring.Store, error) {
	cfg, err := m.store.Config()
	if err != nil {
		return nil, err
	}
	if !cfg.Configured {
		return nil, ErrUnconfigured
	}
	switch cfg.SecretStorageType {
	case models.SecretVault:
		if !m.vault.IsUnlocked() {
			return nil, keyring.ErrLocked
		}
		return m.vault, nil
	case models.SecretLibsecret:
		return m.libsecret, nil
	default:
		return nil, errors.New("unknown secret storage type")
	}
}

// KeyringCollection returns the libsecret collection path when that backend is
// configured, or "" otherwise.
func (m *Manager) KeyringCollection() string {
	cfg, err := m.store.Config()
	if err != nil || cfg.SecretStorageType != models.SecretLibsecret {
		return ""
	}
	path, err := m.libsecret.CollectionPath()
	if err != nil {
		return ""
	}
	return path
}

// Secret returns the real secret for keyID from the active store. It returns
// keyring.ErrLocked when the vault is locked and keyring.ErrNotFound when the
// secret is absent.
func (m *Manager) Secret(keyID string) (string, error) {
	secrets, err := m.Secrets()
	if err != nil {
		return "", err
	}
	return secrets.GetSecret(keyID)
}

// Reset wipes all credentials and returns the store to the unconfigured state.
func (m *Manager) Reset() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	if err := m.vault.Reset(); err != nil {
		errs = append(errs, fmt.Errorf("reset vault: %w", err))
	}
	if m.libsecret.Available() {
		if err := m.libsecret.ClearAll(); err != nil {
			errs = append(errs, fmt.Errorf("clear keyring: %w", err))
		}
	}
	if err := m.store.ClearAPIKeys(); err != nil {
		errs = append(errs, fmt.Errorf("clear api keys: %w", err))
	}
	if err := m.store.SaveConfig(models.Config{}); err != nil {
		errs = append(errs, fmt.Errorf("reset config: %w", err))
	}
	return errors.Join(errs...)
}

// Close zeroes cached key material and releases the D-Bus connection.
func (m *Manager) Close() error {
	_ = m.vault.Lock()
	return m.libsecret.Close()
}
