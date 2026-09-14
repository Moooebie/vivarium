package tui

import (
	"context"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

// endpointsHost binds the API endpoints editor to a concrete owner: a recipe
// being edited in memory, or a live instance.
type endpointsHost interface {
	Endpoints() []models.APIKey
	SetEndpoints([]models.APIKey)
	SavedKeys() []apitypes.APIKeyView
	SetSavedKeys([]apitypes.APIKeyView)
	// AddEndpoint binds a key after validation; it returns an error when the
	// endpoint is rejected (for example a duplicate mock URL).
	AddEndpoint(models.APIKey) error
	// OnChanged is called after any mutation so the owner can mark itself dirty.
	OnChanged()
}

// endpointsDataMsg carries saved keys loaded asynchronously by an owner.
type endpointsDataMsg struct {
	keys []apitypes.APIKeyView
	err  error
}

// inlineKeyCreatedMsg reports a newly created inline API key.
type inlineKeyCreatedMsg struct {
	key models.APIKey
	err error
}

// endpointsEditorScreen manages the API endpoints bound to a recipe or instance.
// It reads and mutates an endpointsHost; persistence is the owner's concern.
type endpointsEditorScreen struct {
	app    *App
	host   endpointsHost
	load   func() tea.Cmd
	commit func() tea.Cmd

	menu     *menu
	mode     string // "", "pick", "inline"
	form     *form
	name     *field
	base     *field
	mock     *field
	secret   *field
	provider models.ProviderType
	picking  bool
	pickMenu *menu
	err      string
}

// newEndpointsEditorScreen builds the editor. load (optional) fetches saved
// keys; commit (optional) is run once when leaving if the host changed.
func newEndpointsEditorScreen(app *App, host endpointsHost, load, commit func() tea.Cmd) *endpointsEditorScreen {
	s := &endpointsEditorScreen{
		app: app, host: host, load: load, commit: commit,
		menu: newMenu(app.theme), provider: models.ProviderCustom,
	}
	s.form = newForm(app.theme)
	s.name = newField(app.theme, "Name", "Input a name…", false)
	s.base = newField(app.theme, "Base URL", "https://api.openai.com/v1", false)
	s.mock = newField(app.theme, "Mock URL", "https://api.openai.com/v1", false)
	s.secret = newField(app.theme, "Secret", "sk-…", false)
	s.form.addField(s.name)
	s.form.addField(s.base)
	s.form.addField(s.mock)
	s.form.addField(s.secret)
	s.form.addAction("provider", "Provider: custom")
	s.form.addButton(
		button{Key: "add", Label: "Add Endpoint", Hotkey: "C-a"},
		button{Key: "test", Label: "Test Key", Hotkey: "C-t"},
		button{Key: "cancel", Label: "Cancel", Hotkey: "Esc"},
	)
	s.pickMenu = newMenu(app.theme)
	items := make([]menuItem, 0, len(providerTypes))
	for _, p := range providerTypes {
		items = append(items, menuItem{Key: string(p), Label: string(p)})
	}
	s.pickMenu.setItems(items)
	s.rebuild()
	return s
}

func (s *endpointsEditorScreen) ID() string    { return "endpoints-editor" }
func (s *endpointsEditorScreen) Title() string { return "API Endpoints" }
func (s *endpointsEditorScreen) Init() tea.Cmd {
	if s.load == nil {
		return nil
	}
	return s.load()
}

func (s *endpointsEditorScreen) rebuild() {
	eps := s.host.Endpoints()
	items := make([]menuItem, 0, len(eps)+2)
	for i, e := range eps {
		items = append(items, menuItem{Key: "ep:" + itoa(i), Label: e.Name, Detail: e.MockURL})
	}
	if len(items) == 0 {
		items = append(items, menuItem{Header: true, Label: "NO ENDPOINTS"})
	}
	items = append(items, menuItem{Key: "pick", Label: "＋ Add from saved key"})
	items = append(items, menuItem{Key: "inline", Label: "＋ Define inline"})
	s.menu.setItems(items)
	s.menu.setSize(s.app.width, maxInt(1, s.app.height-8))
}

// keyItems renders the saved keys available to pick, flagged by secret state.
func (s *endpointsEditorScreen) keyItems() []menuItem {
	keys := s.host.SavedKeys()
	items := make([]menuItem, 0, len(keys))
	for _, k := range keys {
		item := menuItem{Key: k.ID, Label: k.Name, Detail: k.MockURL}
		switch secretStateOf(k) {
		case apitypes.SecretMissing:
			item.Badge = "MISSING"
			item.BadgeColor = lipgloss.Color("214")
		case apitypes.SecretLocked:
			item.Badge = "LOCKED"
			item.BadgeColor = lipgloss.Color("39")
		case apitypes.SecretError:
			item.Badge = "SECRET ERROR"
			item.BadgeColor = lipgloss.Color("203")
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		items = append(items, menuItem{Header: true, Label: "NO SAVED API KEYS — define one inline"})
	}
	return items
}

func (s *endpointsEditorScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if data, ok := msg.(endpointsDataMsg); ok {
		if data.err != nil {
			s.err = data.err.Error()
			return s, nil
		}
		s.host.SetSavedKeys(data.keys)
		s.rebuild()
		return s, nil
	}
	if created, ok := msg.(inlineKeyCreatedMsg); ok {
		if created.err != nil {
			s.err = created.err.Error()
			return s, nil
		}
		s.host.SetSavedKeys(append(s.host.SavedKeys(), apitypes.APIKeyView{
			APIKey: created.key, HasSecret: true, SecretState: apitypes.SecretOK,
		}))
		if err := s.host.AddEndpoint(created.key); err != nil {
			s.err = err.Error()
			return s, nil
		}
		s.host.OnChanged()
		s.mode = ""
		s.rebuild()
		return s, setStatus("defined key "+created.key.Name, false)
	}
	if res, ok := msg.(apiKeyTestMsg); ok {
		return s, tea.Batch(setStatus(testStatus(res.res, res.err), !testOK(res.res, res.err)), setBusy(false, ""))
	}
	switch s.mode {
	case "pick":
		if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
			s.mode = ""
			return s, nil
		}
		if id, ok := s.menu.update(msg); ok {
			if err := s.addFromSaved(id); err != nil {
				s.err = err.Error()
			}
			s.mode = ""
			s.rebuild()
		}
		return s, nil
	case "inline":
		if s.picking {
			if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
				s.picking = false
				return s, nil
			}
			if id, ok := s.pickMenu.update(msg); ok {
				s.provider = models.ProviderType(id)
				s.picking = false
				if def := models.ProviderDefaultURL(s.provider); def != "" {
					s.base.SetValue(def)
					s.mock.SetValue(def)
				}
				s.form.setActionLabel("provider", "Provider: "+string(s.provider))
			}
			return s, nil
		}
		if s.form.Capturing() {
			_, _, cmd := s.form.Update(msg)
			return s, cmd
		}
		if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
			s.mode = ""
			return s, nil
		}
		action, _, cmd := s.form.Update(msg)
		switch action {
		case "add":
			return s, s.addInline()
		case "test":
			return s, s.testInline()
		case "provider":
			s.picking = true
		case "cancel":
			s.mode = ""
		}
		return s, cmd
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			if s.commit != nil {
				return s, tea.Batch(s.commit(), pop())
			}
			return s, pop()
		case "d":
			return s, s.removeSelected()
		}
	}
	if id, ok := s.menu.update(msg); ok {
		switch id {
		case "pick":
			s.mode = "pick"
			s.menu.setItems(s.keyItems())
			s.menu.setSize(s.app.width, maxInt(1, s.app.height-8))
		case "inline":
			s.mode = "inline"
			s.picking = false
			s.provider = models.ProviderCustom
			s.form.setActionLabel("provider", "Provider: custom")
			s.name.SetValue("")
			s.base.SetValue("")
			s.mock.SetValue("")
			s.secret.SetValue("")
		default:
			// endpoint rows are informational
		}
	}
	return s, nil
}

func (s *endpointsEditorScreen) addFromSaved(id string) error {
	for _, k := range s.host.SavedKeys() {
		if k.ID == id {
			if err := s.host.AddEndpoint(k.APIKey); err != nil {
				return err
			}
			s.host.OnChanged()
			return nil
		}
	}
	return nil
}

func (s *endpointsEditorScreen) removeSelected() tea.Cmd {
	id, ok := s.menu.selected()
	if !ok || !strings.HasPrefix(id, "ep:") {
		return nil
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(id, "ep:"))
	eps := s.host.Endpoints()
	if err != nil || idx < 0 || idx >= len(eps) {
		return nil
	}
	s.host.SetEndpoints(append(eps[:idx:idx], eps[idx+1:]...))
	s.host.OnChanged()
	s.rebuild()
	return nil
}

func (s *endpointsEditorScreen) addInline() tea.Cmd {
	name := strings.TrimSpace(s.name.Value())
	if name == "" {
		s.err = "name is required"
		return nil
	}
	if err := validateURL(s.base.Value()); err != nil {
		s.err = "base URL: " + err.Error()
		return nil
	}
	if err := validateURL(s.mock.Value()); err != nil {
		s.err = "mock URL: " + err.Error()
		return nil
	}
	if s.secret.Value() == "" {
		s.err = "secret is required"
		return nil
	}
	s.err = ""
	payload := apitypes.APIKeyPayload{
		APIKey: models.APIKey{Name: name, ProviderType: s.provider,
			BaseURL: strings.TrimSpace(s.base.Value()), MockURL: strings.TrimSpace(s.mock.Value())},
		Secret: s.secret.Value(),
	}
	return func() tea.Msg {
		key, err := s.app.ctx.Client.CreateAPIKey(context.Background(), payload)
		return inlineKeyCreatedMsg{key: key, err: err}
	}
}

func (s *endpointsEditorScreen) testInline() tea.Cmd {
	if err := validateURL(s.base.Value()); err != nil {
		s.err = "base URL: " + err.Error()
		return nil
	}
	if s.secret.Value() == "" {
		s.err = "secret is required to test"
		return nil
	}
	s.err = ""
	req := apitypes.TestConnectionRequest{
		ProviderType: s.provider,
		BaseURL:      strings.TrimSpace(s.base.Value()),
		Secret:       s.secret.Value(),
	}
	return tea.Batch(setBusy(true, "Testing key"), func() tea.Msg {
		res, err := s.app.ctx.Client.TestAPIKeyPayload(context.Background(), req)
		return apiKeyTestMsg{res: res, err: err}
	})
}

func (s *endpointsEditorScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render("API Endpoints") + "\n\n")
	if s.err != "" {
		b.WriteString(t.Error.Render(s.err) + "\n\n")
	}
	switch s.mode {
	case "pick":
		b.WriteString("Add from saved key:\n\n" + s.menu.view() + "\n\n" + t.Dim.Render("Enter add · Esc cancel"))
	case "inline":
		if s.picking {
			b.WriteString("Provider type:\n\n" + s.pickMenu.view() + "\n\n" + t.Dim.Render("Enter select · Esc cancel"))
			return b.String()
		}
		s.form.setTop(2)
		b.WriteString("Define inline API key:\n\n" + s.form.view() + "\n\n" +
			t.Dim.Render("Enter edit/activate · ↑↓ move · Esc cancel"))
	default:
		s.menu.setTop(2)
		b.WriteString(s.menu.view())
		b.WriteString("\n\n" + s.app.helpBar("d remove", "Esc back"))
	}
	return b.String()
}
