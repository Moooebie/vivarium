package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"vivarium/internal/apitypes"
)

type mainScreen struct {
	app     *App
	menu    *menu
	status  apitypes.SystemStatusResponse
	running int
	err     string
}

type mainDataMsg struct {
	status  apitypes.SystemStatusResponse
	running int
	err     error
}

func newMainScreen(app *App) *mainScreen {
	s := &mainScreen{app: app}
	s.menu = newMenu(app.theme)
	s.menu.setItems([]menuItem{
		{Key: "instances", Hotkey: "1", Label: "Instances", Detail: "Manage running and halted sandboxes"},
		{Key: "recipes", Hotkey: "2", Label: "Recipes", Detail: "View and configure blueprints"},
		{Key: "keys", Hotkey: "3", Label: "API Keys", Detail: "Manage upstream credentials and mock routing"},
		{Key: "images", Hotkey: "4", Label: "Base Images", Detail: "Manage and build guest images"},
		{Key: "quit", Hotkey: "Q", Label: "Quit", Detail: "Exit Vivarium"},
	})
	return s
}

func (s *mainScreen) ID() string    { return "main" }
func (s *mainScreen) Title() string { return "Main" }

// Reload implements reloader.
func (s *mainScreen) Reload() tea.Cmd { return s.Init() }

func (s *mainScreen) Init() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		status, err := s.app.ctx.Client.SystemStatus(ctx)
		if err != nil {
			return mainDataMsg{err: err}
		}
		running := 0
		if views, err := s.app.ctx.Client.ListInstances(ctx); err == nil {
			for _, v := range views {
				if v.Status == "running" {
					running++
				}
			}
		}
		return mainDataMsg{status: status, running: running}
	}
}

func (s *mainScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case mainDataMsg:
		s.err = ""
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, nil
		}
		s.status, s.running = msg.status, msg.running
		return s, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "1":
			return s, push(newInstancesScreen(s.app))
		case "2":
			return s, push(newRecipesScreen(s.app))
		case "3":
			return s, push(newAPIKeysScreen(s.app))
		case "4":
			return s, push(newBaseImagesScreen(s.app))
		case "q", "Q":
			return s, tea.Quit
		}
		if action, ok := s.menu.update(msg); ok {
			switch action {
			case "instances":
				return s, push(newInstancesScreen(s.app))
			case "recipes":
				return s, push(newRecipesScreen(s.app))
			case "keys":
				return s, push(newAPIKeysScreen(s.app))
			case "images":
				return s, push(newBaseImagesScreen(s.app))
			case "quit":
				return s, tea.Quit
			}
		}
	}
	return s, nil
}

func (s *mainScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	if s.app.ctx.Logo != "" {
		b.WriteString(s.app.ctx.Logo)
		if !strings.HasSuffix(s.app.ctx.Logo, "\n") {
			b.WriteString("\n")
		}
	} else {
		b.WriteString(t.Title.Render("V I V A R I U M") + "\n")
	}
	b.WriteString("\n")
	b.WriteString(t.Subtitle.Render("version "+s.app.ctx.Version+
		"   socket "+s.app.ctx.Env.SocketPath()) + "\n")
	mode := s.status.EncryptionMode
	if mode == "" {
		mode = "unconfigured"
	}
	b.WriteString(t.Subtitle.Render("containers running: "+itoa(s.running)+
		"   encryption: "+mode) + "\n")
	if s.err != "" {
		b.WriteString(t.Error.Render("backend: "+s.err) + "\n")
	}
	b.WriteString("\n")
	s.menu.setSize(s.app.width, len(s.menu.items))
	b.WriteString(s.menu.view())
	b.WriteString("\n\n" + s.app.helpBar("↑/↓ select", "Enter open", "q quit"))
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
