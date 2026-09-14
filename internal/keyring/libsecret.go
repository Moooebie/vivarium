package keyring

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// FreeDesktop Secret Service constants.
const (
	secretServiceName = "org.freedesktop.secrets"
	secretServicePath = dbus.ObjectPath("/org/freedesktop/secrets")
	ifaceService      = "org.freedesktop.Secret.Service"
	ifaceCollection   = "org.freedesktop.Secret.Collection"
	ifaceItem         = "org.freedesktop.Secret.Item"
	ifacePrompt       = "org.freedesktop.Secret.Prompt"

	// SchemaName is the credential schema used by Vivarium.
	SchemaName = "org.vivarium.ApiKey"
	// AttrKeyID is the lookup attribute mapping a Vivarium key ID to a secret.
	AttrKeyID = "vivarium_key_id"
	// attrMarker/attrMarkerValue is a stable namespace marker present on every
	// Vivarium item, used for bulk operations independent of the schema.
	attrMarker      = "vivarium"
	attrMarkerValue = "1"

	promptTimeout = 30 * time.Second
)

// dbusSecret mirrors the (oayays) Secret struct.
type dbusSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// LibSecret implements Store using the FreeDesktop Secret Service over D-Bus.
type LibSecret struct {
	mu      sync.Mutex
	conn    *dbus.Conn
	session dbus.ObjectPath
	initErr error
}

// NewLibSecret connects to the session bus and opens a plain session. A
// connection error is deferred until first use so that constructing the
// backend never panics; call Available to probe eagerly.
func NewLibSecret() *LibSecret { return &LibSecret{} }

// Kind implements Store.
func (l *LibSecret) Kind() Kind { return KindLibsecret }

// Available reports whether a Secret Service is reachable.
func (l *LibSecret) Available() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ensureSessionLocked() == nil
}

// ensureSessionLocked lazily connects and opens a session. Caller holds l.mu.
func (l *LibSecret) ensureSessionLocked() error {
	if l.conn != nil && l.session != "" {
		return nil
	}
	if l.initErr != nil {
		return l.initErr
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		l.initErr = fmt.Errorf("%w: %v", ErrUnavailable, err)
		return l.initErr
	}
	obj := conn.Object(secretServiceName, secretServicePath)
	var output dbus.Variant
	var session dbus.ObjectPath
	if err := obj.Call(ifaceService+".OpenSession", 0, "plain", dbus.MakeVariant("")).
		Store(&output, &session); err != nil {
		l.initErr = fmt.Errorf("%w: open session: %v", ErrUnavailable, err)
		return l.initErr
	}
	if session == "/" || session == "" {
		l.initErr = fmt.Errorf("%w: interactive unlock required", ErrUnavailable)
		return l.initErr
	}
	l.conn = conn
	l.session = session
	return nil
}

// StoreSecret writes or replaces the secret for keyID.
func (l *LibSecret) StoreSecret(keyID, secret string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureSessionLocked(); err != nil {
		return err
	}
	props := map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant("vivarium:" + keyID),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(l.attributes(keyID)),
	}
	sec := dbusSecret{
		Session:     l.session,
		Parameters:  []byte{},
		Value:       []byte(secret),
		ContentType: "text/plain",
	}
	// CreateItem is a method of the Collection interface, not the Service.
	collection, err := l.persistentCollectionLocked()
	if err != nil {
		return err
	}
	colObj := l.conn.Object(secretServiceName, collection)
	var item, prompt dbus.ObjectPath
	if err := colObj.Call(ifaceCollection+".CreateItem", 0, props, sec, true).
		Store(&item, &prompt); err != nil {
		return fmt.Errorf("create secret: %w", err)
	}
	if err := l.handlePromptLocked(prompt); err != nil {
		return err
	}
	// Verify the secret is actually retrievable before reporting success, so a
	// failed write never leaves metadata pointing at a missing secret.
	got, err := l.getSecretLocked(keyID)
	if err != nil {
		return fmt.Errorf("stored secret is not retrievable: %w", err)
	}
	if got != secret {
		return errors.New("stored secret did not match the value written")
	}
	return nil
}

// persistentCollectionLocked resolves a persistent collection for new items. It
// prefers the login/default aliases and never returns the ephemeral session
// collection.
func (l *LibSecret) persistentCollectionLocked() (dbus.ObjectPath, error) {
	session := l.readAliasLocked("session")
	login := l.readAliasLocked("login")
	def := l.readAliasLocked("default")
	path, ok := choosePersistentCollection(session, login, def, l.collectionsLocked())
	if !ok {
		return "", fmt.Errorf("%w: no persistent keyring collection available (only the ephemeral session collection)", ErrUnavailable)
	}
	return path, nil
}

// choosePersistentCollection picks a non-session collection, preferring the
// login then default aliases and otherwise the first listed collection.
func choosePersistentCollection(session, login, def dbus.ObjectPath, collections []dbus.ObjectPath) (dbus.ObjectPath, bool) {
	usable := func(c dbus.ObjectPath) bool {
		return c != "" && c != "/" && c != session
	}
	for _, c := range []dbus.ObjectPath{login, def} {
		if usable(c) {
			return c, true
		}
	}
	for _, c := range collections {
		if usable(c) {
			return c, true
		}
	}
	return "", false
}

func (l *LibSecret) collectionsLocked() []dbus.ObjectPath {
	var variant dbus.Variant
	if err := l.conn.Object(secretServiceName, secretServicePath).
		Call("org.freedesktop.DBus.Properties.Get", 0, ifaceService, "Collections").
		Store(&variant); err != nil {
		return nil
	}
	collections, _ := variant.Value().([]dbus.ObjectPath)
	return collections
}

// readAliasLocked returns the collection for an alias, or "" when unset/absent.
func (l *LibSecret) readAliasLocked(alias string) dbus.ObjectPath {
	var path dbus.ObjectPath
	if err := l.conn.Object(secretServiceName, secretServicePath).
		Call(ifaceService+".ReadAlias", 0, alias).Store(&path); err != nil {
		return ""
	}
	return path
}

// CollectionPath returns the persistent collection used for storage.
func (l *LibSecret) CollectionPath() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureSessionLocked(); err != nil {
		return "", err
	}
	path, err := l.persistentCollectionLocked()
	if err != nil {
		return "", err
	}
	return string(path), nil
}

// GetSecret returns the secret for keyID.
func (l *LibSecret) GetSecret(keyID string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.getSecretLocked(keyID)
}

// getSecretLocked reads the secret for keyID. Caller holds l.mu.
func (l *LibSecret) getSecretLocked(keyID string) (string, error) {
	items, err := l.findItemsLocked(keyID)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", ErrNotFound
	}
	item := l.conn.Object(secretServiceName, items[0])
	var sec dbusSecret
	if err := item.Call(ifaceItem+".GetSecret", 0, l.session).Store(&sec); err != nil {
		return "", fmt.Errorf("get secret: %w", err)
	}
	return string(sec.Value), nil
}

// DeleteSecret removes the secret for keyID.
func (l *LibSecret) DeleteSecret(keyID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	items, err := l.findItemsLocked(keyID)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return ErrNotFound
	}
	for _, path := range items {
		item := l.conn.Object(secretServiceName, path)
		var prompt dbus.ObjectPath
		if err := item.Call(ifaceItem+".Delete", 0).Store(&prompt); err != nil {
			return fmt.Errorf("delete secret: %w", err)
		}
		if err := l.handlePromptLocked(prompt); err != nil {
			return err
		}
	}
	return nil
}

// Close releases the D-Bus connection.
func (l *LibSecret) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn != nil {
		err := l.conn.Close()
		l.conn = nil
		l.session = ""
		return err
	}
	return nil
}

// attributes returns the attributes written when creating an item.
func (l *LibSecret) attributes(keyID string) map[string]string {
	return map[string]string{
		attrMarker:   attrMarkerValue,
		AttrKeyID:    keyID,
		"xdg:schema": SchemaName,
	}
}

// lookupAttrs returns the attributes used to find an item. It deliberately
// omits xdg:schema: some services normalise it, so lookups key only on the
// Vivarium key ID.
func lookupAttrs(keyID string) map[string]string {
	return map[string]string{AttrKeyID: keyID}
}

// findItemsLocked searches for items matching keyID, unlocking if necessary.
func (l *LibSecret) findItemsLocked(keyID string) ([]dbus.ObjectPath, error) {
	return l.searchLocked(lookupAttrs(keyID))
}

// ClearAll removes every Vivarium secret. It is used by the credential reset
// routine and matches both the marker attribute and the legacy schema.
func (l *LibSecret) ClearAll() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := map[dbus.ObjectPath]struct{}{}
	var items []dbus.ObjectPath
	for _, attrs := range []map[string]string{
		{attrMarker: attrMarkerValue},
		{"xdg:schema": SchemaName},
	} {
		found, err := l.searchLocked(attrs)
		if err != nil {
			return err
		}
		for _, p := range found {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			items = append(items, p)
		}
	}
	for _, path := range items {
		item := l.conn.Object(secretServiceName, path)
		var prompt dbus.ObjectPath
		if err := item.Call(ifaceItem+".Delete", 0).Store(&prompt); err != nil {
			return fmt.Errorf("delete secret: %w", err)
		}
		if err := l.handlePromptLocked(prompt); err != nil {
			return err
		}
	}
	return nil
}

// searchLocked searches for items matching attrs, unlocking if necessary.
func (l *LibSecret) searchLocked(attrs map[string]string) ([]dbus.ObjectPath, error) {
	if err := l.ensureSessionLocked(); err != nil {
		return nil, err
	}
	obj := l.conn.Object(secretServiceName, secretServicePath)
	var unlocked, locked []dbus.ObjectPath
	if err := obj.Call(ifaceService+".SearchItems", 0, attrs).
		Store(&unlocked, &locked); err != nil {
		return nil, fmt.Errorf("search secrets: %w", err)
	}
	if len(unlocked) == 0 && len(locked) > 0 {
		var newlyUnlocked []dbus.ObjectPath
		var prompt dbus.ObjectPath
		if err := obj.Call(ifaceService+".Unlock", 0, locked).
			Store(&newlyUnlocked, &prompt); err != nil {
			return nil, fmt.Errorf("unlock secrets: %w", err)
		}
		if err := l.handlePromptLocked(prompt); err != nil {
			return nil, err
		}
		unlocked = append(unlocked, newlyUnlocked...)
	}
	return unlocked, nil
}

// handlePromptLocked waits for an interactive prompt to complete, if one was
// returned by the Secret Service.
func (l *LibSecret) handlePromptLocked(prompt dbus.ObjectPath) error {
	if prompt == "/" || prompt == "" {
		return nil
	}
	sigCh := make(chan *dbus.Signal, 8)
	l.conn.Signal(sigCh)
	defer l.conn.RemoveSignal(sigCh)

	obj := l.conn.Object(secretServiceName, prompt)
	if err := obj.Call(ifacePrompt+".Prompt", 0, "").Err; err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	timer := time.NewTimer(promptTimeout)
	defer timer.Stop()
	for {
		select {
		case sig := <-sigCh:
			if sig == nil || sig.Path != prompt || sig.Name != ifacePrompt+".Completed" {
				continue
			}
			if len(sig.Body) > 0 {
				if dismissed, ok := sig.Body[0].(bool); ok && dismissed {
					return errors.New("secret service prompt dismissed")
				}
			}
			return nil
		case <-timer.C:
			return errors.New("secret service prompt timed out")
		}
	}
}
