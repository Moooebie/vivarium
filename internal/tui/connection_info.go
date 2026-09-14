package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"vivarium/internal/apitypes"
)

// connectionInfoScreen shows how an agent reaches the instance's endpoints:
// the mock URL to query and the dummy token to authenticate with. Standard
// providers need no manual configuration; custom endpoints do.
type connectionInfoScreen struct {
	app  *App
	inst apitypes.InstanceView
}

func newConnectionInfoScreen(app *App, inst apitypes.InstanceView) *connectionInfoScreen {
	return &connectionInfoScreen{app: app, inst: inst}
}

func (s *connectionInfoScreen) ID() string    { return "connection-info" }
func (s *connectionInfoScreen) Title() string { return "Connection Info" }
func (s *connectionInfoScreen) Init() tea.Cmd { return nil }

func (s *connectionInfoScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc", "enter":
			return s, pop()
		}
	}
	return s, nil
}

func (s *connectionInfoScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render("Connection Info — "+s.inst.Name) + "\n\n")

	if len(s.inst.Endpoints) == 0 {
		b.WriteString(t.Dim.Render("No API endpoints are attached to this instance.") + "\n")
		b.WriteString("\n" + s.app.helpBar("Esc back"))
		return b.String()
	}

	b.WriteString(t.Dim.Render(
		"Agents reach these endpoints through the host bridge. Standard providers\n"+
			"work automatically; for custom endpoints configure the agent with the URL\n"+
			"and token below.") + "\n\n")

	for i, ep := range s.inst.Endpoints {
		b.WriteString(t.Header.Render(strings.ToUpper(string(ep.ProviderType))) + "\n")
		b.WriteString("  " + t.FieldLabel.Render("Mock URL") + ": " + ep.MockURL + "\n")
		b.WriteString("  " + t.FieldLabel.Render("Token") + ":    " + ep.Token + "\n")
		if i < len(s.inst.Endpoints)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n" + s.app.helpBar("Esc back"))
	return b.String()
}
