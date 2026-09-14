package docker

import (
	"archive/tar"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestBuildTarPathCreatesParentDirs(t *testing.T) {
	data, err := buildTarPath("/usr/local/share/ca-certificates/vivarium-ca.crt", []byte("x"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(data))
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	want := []string{
		"usr/", "usr/local/", "usr/local/share/", "usr/local/share/ca-certificates/",
		"usr/local/share/ca-certificates/vivarium-ca.crt",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v", names, want)
	}
}

func TestBuildTarPathRejectsEmpty(t *testing.T) {
	if _, err := buildTarPath("/", []byte("x"), 0o644); err == nil {
		t.Fatal("expected error for root path")
	}
}
