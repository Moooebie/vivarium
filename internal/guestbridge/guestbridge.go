// Package guestbridge embeds the small TCP relay injected into agent
// containers and exposes its metadata.
package guestbridge

import (
	_ "embed"
	"runtime"
)

// GuestPath is where the relay is injected inside a container.
const GuestPath = "/usr/local/bin/vivarium-guestbridge"

//go:embed bin/vivarium-guestbridge
var binary []byte

// Binary returns the relay executable. The slice is shared; callers must not
// mutate it.
func Binary() []byte { return binary }

// Supported reports whether a relay binary is available for this host.
func Supported() bool {
	return len(binary) > 0 && runtime.GOOS == "linux" && runtime.GOARCH == "amd64"
}
