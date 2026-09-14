// Package keyring provides interchangeable credential storage. Two backends
// implement the Store interface: libsecret (FreeDesktop Secret Service over
// D-Bus) and a local encrypted vault (Argon2id + AES-256-GCM).
package keyring

import "errors"

// Kind identifies a backend implementation.
type Kind string

const (
	// KindLibsecret is the FreeDesktop Secret Service backend.
	KindLibsecret Kind = "libsecret"
	// KindVault is the local encrypted vault backend.
	KindVault Kind = "vault"
)

// Sentinel errors returned by Store implementations.
var (
	// ErrNotFound indicates no secret exists for the given key ID.
	ErrNotFound = errors.New("secret not found")
	// ErrLocked indicates the store must be unlocked before use.
	ErrLocked = errors.New("secret store is locked")
	// ErrUnavailable indicates the backend cannot be reached.
	ErrUnavailable = errors.New("secret service unavailable")
	// ErrAlreadyExists indicates a vault already exists during setup.
	ErrAlreadyExists = errors.New("secret store already configured")
)

// Store abstracts credential persistence keyed by Vivarium key ID.
type Store interface {
	// Kind returns the backend kind.
	Kind() Kind
	// StoreSecret writes or replaces the secret for keyID.
	StoreSecret(keyID, secret string) error
	// GetSecret returns the secret for keyID, or ErrNotFound.
	GetSecret(keyID string) (string, error)
	// DeleteSecret removes the secret for keyID.
	DeleteSecret(keyID string) error
	// Close releases any held resources and zeroes cached key material.
	Close() error
}
