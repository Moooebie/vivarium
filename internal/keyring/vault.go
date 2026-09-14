package keyring

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/sys/unix"
)

// Vault cryptographic parameters (see BACKEND.md §2.1).
const (
	vaultMagic    = "VIVVLT01"
	saltLen       = 16
	nonceLen      = 12
	vaultKeyLen   = 32
	argonMemory   = 64 * 1024 // 64 MB, in KiB
	argonTime     = 3
	argonThreads  = 4
	vaultFilePerm = 0o600
)

// Vault is an Argon2id + AES-256-GCM encrypted map of key ID to secret.
type Vault struct {
	path string

	mu       sync.Mutex
	key      []byte
	salt     []byte
	data     map[string]string
	unlocked bool
	locked   bool // memory-lock state for key
}

// NewVault returns a Vault persisted at path. The vault starts locked.
func NewVault(path string) *Vault { return &Vault{path: path} }

// Path returns the vault file location.
func (v *Vault) Path() string { return v.path }

// Kind implements Store.
func (v *Vault) Kind() Kind { return KindVault }

// Exists reports whether a vault file is present.
func (v *Vault) Exists() bool {
	_, err := os.Stat(v.path)
	return err == nil
}

// IsUnlocked reports whether the vault key is currently held in memory.
func (v *Vault) IsUnlocked() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.unlocked
}

// Setup initialises a new, empty vault protected by password. It fails if a
// vault already exists.
func (v *Vault) Setup(password string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if password == "" {
		return errors.New("master password must not be empty")
	}
	if _, err := os.Stat(v.path); err == nil {
		return ErrAlreadyExists
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, vaultKeyLen)
	v.lockKey(key)
	v.key = key
	v.salt = salt
	v.data = map[string]string{}
	v.unlocked = true
	return v.persistLocked()
}

// Unlock derives the key from password and decrypts the vault.
func (v *Vault) Unlock(password string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.unlocked {
		return nil
	}
	raw, err := os.ReadFile(v.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	salt, nonce, ciphertext, err := parseVaultFile(raw)
	if err != nil {
		return err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, vaultKeyLen)
	plaintext, err := decrypt(key, nonce, ciphertext)
	if err != nil {
		zero(key)
		return errors.New("invalid master password")
	}
	var data map[string]string
	if err := json.Unmarshal(plaintext, &data); err != nil {
		zero(key)
		zero(plaintext)
		return fmt.Errorf("corrupt vault: %w", err)
	}
	zero(plaintext)
	v.lockKey(key)
	v.key = key
	v.salt = salt
	v.data = data
	v.unlocked = true
	return nil
}

// Lock zeroes the in-memory key and drops decrypted contents.
func (v *Vault) Lock() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lockLocked()
}

func (v *Vault) lockLocked() error {
	if v.key != nil {
		if v.locked {
			_ = unix.Munlock(v.key)
		}
		zero(v.key)
	}
	v.key = nil
	v.salt = nil
	v.data = nil
	v.unlocked = false
	v.locked = false
	return nil
}

// Close implements Store and locks the vault.
func (v *Vault) Close() error { return v.Lock() }

// StoreSecret writes or replaces a secret.
func (v *Vault) StoreSecret(keyID, secret string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.unlocked {
		return ErrLocked
	}
	if v.data == nil {
		v.data = map[string]string{}
	}
	v.data[keyID] = secret
	return v.persistLocked()
}

// GetSecret returns the secret for keyID.
func (v *Vault) GetSecret(keyID string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.unlocked {
		return "", ErrLocked
	}
	secret, ok := v.data[keyID]
	if !ok {
		return "", ErrNotFound
	}
	return secret, nil
}

// DeleteSecret removes the secret for keyID.
func (v *Vault) DeleteSecret(keyID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.unlocked {
		return ErrLocked
	}
	if _, ok := v.data[keyID]; !ok {
		return ErrNotFound
	}
	delete(v.data, keyID)
	return v.persistLocked()
}

// Reset wipes the vault file and clears all in-memory key material.
func (v *Vault) Reset() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	_ = v.lockLocked()
	if err := os.Remove(v.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// persistLocked serialises and encrypts the vault. Caller holds v.mu.
func (v *Vault) persistLocked() error {
	plaintext, err := json.Marshal(v.data)
	if err != nil {
		return err
	}
	defer zero(plaintext)

	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ciphertext, err := encrypt(v.key, nonce, plaintext)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString(vaultMagic)
	buf.Write(v.salt)
	buf.Write(nonce)
	buf.Write(ciphertext)

	if err := os.MkdirAll(filepath.Dir(v.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(v.path), ".vault-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(vaultFilePerm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
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
	return os.Rename(tmpName, v.path)
}

// lockKey attempts to pin key material in RAM. Failure is non-fatal (for
// example under a restrictive RLIMIT_MEMLOCK).
func (v *Vault) lockKey(key []byte) {
	if len(key) == 0 {
		return
	}
	if err := unix.Mlock(key); err == nil {
		v.locked = true
	}
}

func parseVaultFile(raw []byte) (salt, nonce, ciphertext []byte, err error) {
	if len(raw) < len(vaultMagic)+saltLen+nonceLen {
		return nil, nil, nil, errors.New("vault file is truncated")
	}
	if string(raw[:len(vaultMagic)]) != vaultMagic {
		return nil, nil, nil, errors.New("vault file has an unrecognised format")
	}
	rest := raw[len(vaultMagic):]
	salt = append([]byte(nil), rest[:saltLen]...)
	nonce = append([]byte(nil), rest[saltLen:saltLen+nonceLen]...)
	ciphertext = append([]byte(nil), rest[saltLen+nonceLen:]...)
	return salt, nonce, ciphertext, nil
}

func encrypt(key, nonce, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, []byte(vaultMagic)), nil
}

func decrypt(key, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, []byte(vaultMagic))
}

// zero overwrites b with zeros.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
