package keyring

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestLookupAttrsExcludeSchema(t *testing.T) {
	attrs := lookupAttrs("abc")
	if attrs[AttrKeyID] != "abc" {
		t.Fatalf("key id attr = %q", attrs[AttrKeyID])
	}
	if _, ok := attrs["xdg:schema"]; ok {
		t.Fatal("lookups must not depend on xdg:schema")
	}
}

func TestCreateAttributesHaveMarker(t *testing.T) {
	l := &LibSecret{}
	attrs := l.attributes("abc")
	if attrs[attrMarker] != attrMarkerValue {
		t.Fatalf("marker = %q", attrs[attrMarker])
	}
	if attrs[AttrKeyID] != "abc" {
		t.Fatalf("key id = %q", attrs[AttrKeyID])
	}
	if attrs["xdg:schema"] != SchemaName {
		t.Fatalf("schema = %q", attrs["xdg:schema"])
	}
}

func TestChoosePersistentCollection(t *testing.T) {
	session := dbus.ObjectPath("/org/freedesktop/secrets/collection/session")
	login := dbus.ObjectPath("/org/freedesktop/secrets/collection/login")
	def := dbus.ObjectPath("/org/freedesktop/secrets/collection/default")
	other := dbus.ObjectPath("/org/freedesktop/secrets/collection/vivarium")

	if got, ok := choosePersistentCollection(session, login, "", []dbus.ObjectPath{login, session}); !ok || got != login {
		t.Fatalf("login = %q ok=%v", got, ok)
	}
	if got, ok := choosePersistentCollection(session, "", def, nil); !ok || got != def {
		t.Fatalf("default = %q ok=%v", got, ok)
	}
	if _, ok := choosePersistentCollection(session, session, session, []dbus.ObjectPath{session}); ok {
		t.Fatal("session collection must never be chosen")
	}
	if _, ok := choosePersistentCollection(session, "/", "", []dbus.ObjectPath{"/"}); ok {
		t.Fatal("root path must not be chosen")
	}
	if got, ok := choosePersistentCollection(session, "", "", []dbus.ObjectPath{session, other}); !ok || got != other {
		t.Fatalf("fallback = %q ok=%v", got, ok)
	}
}
