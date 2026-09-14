package tui

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

// embeddedLogo is used when no ./LOGO file can be found, so the Main screen
// always renders branding.
//
//go:embed logo.txt
var embeddedLogo string

// readLogo prefers ./LOGO, then LOGO next to the executable, then the embedded
// fallback.
func readLogo() string {
	candidates := []string{"LOGO"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "LOGO"))
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil && len(bytes.TrimSpace(data)) > 0 {
			return string(data)
		}
	}
	return embeddedLogo
}
