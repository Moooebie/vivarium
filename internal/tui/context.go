package tui

import (
	"vivarium/internal/paths"
)

// Context holds the shared dependencies available to every screen.
type Context struct {
	Client  API
	Env     paths.Env
	Version string
	Cwd     string
	Logo    string
}
