// Package tui implements the Vivarium terminal user interface.
package tui

import (
	"context"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"vivarium/internal/paths"
)

// Options configures the TUI.
type Options struct {
	Client  API
	Env     paths.Env
	Version string
}

// Run starts the interactive TUI and blocks until it exits.
func Run(ctx context.Context, opts Options) error {
	cwd, _ := os.Getwd()
	c := &Context{
		Client:  opts.Client,
		Env:     opts.Env,
		Version: opts.Version,
		Cwd:     cwd,
		Logo:    readLogo(),
	}
	p := tea.NewProgram(newApp(c),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
	)
	_, err := p.Run()
	return err
}
