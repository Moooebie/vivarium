package tui

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

var providerTypes = []models.ProviderType{
	models.ProviderOpenAI, models.ProviderAnthropic, models.ProviderDeepSeek,
	models.ProviderOpenRouter, models.ProviderCustom,
}

type apiKeysScreen struct {
	app  *App
	menu *menu
	keys []apitypes.APIKeyView
	err  string
}

type apiKeysLoadedMsg struct {
	keys []apitypes.APIKeyView
	err  error
}

type apiKeysActionMsg struct {
	status string
	err    error
}

type apiKeysTestMsg struct {
	name string
	res  apitypes.TestConnectionResponse
	err  error
}

func newAPIKeysScreen(app *App) *apiKeysScreen {
	return &apiKeysScreen{app: app, menu: newMenu(app.theme)}
}

func (s *apiKeysScreen) ID() string    { return "apikeys" }
func (s *apiKeysScreen) Title() string { return "API Keys" }
func (s *apiKeysScreen) Init() tea.Cmd { return s.load() }

// Reload implements reloader.
func (s *apiKeysScreen) Reload() tea.Cmd { return s.load() }

func (s *apiKeysScreen) load() tea.Cmd {
	return func() tea.Msg {
		keys, err := s.app.ctx.Client.ListAPIKeys(context.Background())
		return apiKeysLoadedMsg{keys: keys, err: err}
	}
}

func (s *apiKeysScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case apiKeysLoadedMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, nil
		}
		s.err = ""
		s.keys = msg.keys
		items := make([]menuItem, 0, len(msg.keys))
		for _, k := range msg.keys {
			detail := string(k.ProviderType) + "  ·  " + k.MockURL
			if k.RateLimitRPM > 0 {
				detail += "  ·  " + strconv.Itoa(k.RateLimitRPM) + " rpm"
			}
			item := menuItem{Key: "key:" + k.ID, Label: k.Name, Detail: detail}
			if !k.HasSecret {
				item.Badge = "NO SECRET"
				item.BadgeColor = lipgloss.Color("203")
			}
			items = append(items, item)
		}
		if len(items) == 0 {
			items = append(items, menuItem{Header: true, Label: "NO API KEYS — press n to add one"})
		}
		s.menu.setItems(items)
		s.menu.setSize(s.app.width, maxInt(1, s.app.height-6))
		return s, nil
	case apiKeysActionMsg:
		cmd := s.load()
		if msg.err != nil {
			return s, tea.Batch(cmd, setStatus(msg.status+": "+msg.err.Error(), true), setBusy(false, ""))
		}
		return s, tea.Batch(cmd, setStatus(msg.status, false), setBusy(false, ""))
	case apiKeysTestMsg:
		return s, tea.Batch(setStatus(msg.name+": "+testStatus(msg.res, msg.err), !testOK(msg.res, msg.err)), setBusy(false, ""))
	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s *apiKeysScreen) selected() (apitypes.APIKeyView, bool) {
	key, ok := s.menu.selected()
	if !ok || !strings.HasPrefix(key, "key:") {
		return apitypes.APIKeyView{}, false
	}
	id := strings.TrimPrefix(key, "key:")
	for _, k := range s.keys {
		if k.ID == id {
			return k, true
		}
	}
	return apitypes.APIKeyView{}, false
}

func (s *apiKeysScreen) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return s, pop()
	case "r":
		return s, s.load()
	case "n":
		return s, push(newAPIKeyFormScreen(s.app, nil))
	case "e", "enter":
		if k, ok := s.selected(); ok {
			key := k
			return s, push(newAPIKeyFormScreen(s.app, &key))
		}
	case "d":
		if k, ok := s.selected(); ok {
			id, name := k.ID, k.Name
			return s, confirm("Delete API key "+name+"?", func() tea.Cmd { return s.deleteKey(id) })
		}
	case "t":
		if k, ok := s.selected(); ok {
			return s, s.testKey(k.APIKey)
		}
	}
	if _, ok := s.menu.update(msg); ok {
		if k, ok := s.selected(); ok {
			key := k
			return s, push(newAPIKeyFormScreen(s.app, &key))
		}
	}
	return s, nil
}

func (s *apiKeysScreen) deleteKey(id string) tea.Cmd {
	return func() tea.Msg {
		err := s.app.ctx.Client.DeleteAPIKey(context.Background(), id)
		return apiKeysActionMsg{status: "key deleted", err: err}
	}
}

func (s *apiKeysScreen) testKey(k models.APIKey) tea.Cmd {
	return tea.Batch(setBusy(true, "Testing "+k.Name), func() tea.Msg {
		res, err := s.app.ctx.Client.TestAPIKey(context.Background(), k.ID)
		return apiKeysTestMsg{name: k.Name, res: res, err: err}
	})
}

func (s *apiKeysScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render("API Keys") + "\n\n")
	if s.err != "" {
		b.WriteString(t.Error.Render(s.err) + "\n\n")
	}
	s.menu.setTop(2)
	b.WriteString(s.menu.view())
	b.WriteString("\n\n" + s.app.helpBar("n new", "e edit", "d delete", "t test", "r refresh", "Esc back"))
	return b.String()
}

// ---- API key form ----

type apiKeyFormScreen struct {
	app             *App
	editing         *apitypes.APIKeyView
	form            *form
	name            *field
	baseURL         *field
	mockURL         *field
	secret          *field
	rate            *field
	provider        models.ProviderType
	initialProvider models.ProviderType
	picking         bool
	pickMenu        *menu
	err             string
}

type apiKeySavedMsg struct {
	key models.APIKey
	err error
}

type apiKeySecretMsg struct{ secret string }

type apiKeyTestMsg struct {
	res apitypes.TestConnectionResponse
	err error
}

func newAPIKeyFormScreen(app *App, editing *apitypes.APIKeyView) *apiKeyFormScreen {
	s := &apiKeyFormScreen{app: app, editing: editing, provider: models.ProviderOpenAI}
	s.form = newForm(app.theme)
	s.name = newField(app.theme, "Name", "Input a name…", false)
	s.baseURL = newField(app.theme, "Base URL", "https://api.openai.com/v1", false)
	s.mockURL = newField(app.theme, "Mock URL", "https://api.openai.com/v1", false)
	s.secret = newField(app.theme, "Secret", "sk-…", false)
	s.rate = newField(app.theme, "Rate Limit (RPM)", "60", false)
	s.form.addField(s.name)
	s.form.addField(s.baseURL)
	s.form.addField(s.mockURL)
	s.form.addField(s.secret)
	s.form.addField(s.rate)
	s.form.addAction("provider", "Provider")

	buttons := []button{
		{Key: "save", Label: "Save Key", Hotkey: "C-s"},
		{Key: "test", Label: "Test Connection", Hotkey: "C-t"},
	}
	if editing != nil {
		buttons = append(buttons, button{Key: "delete", Label: "Delete Key", Hotkey: "C-d"})
	}
	s.form.addButton(buttons...)

	s.pickMenu = newMenu(app.theme)
	items := make([]menuItem, 0, len(providerTypes))
	for _, p := range providerTypes {
		items = append(items, menuItem{Key: string(p), Label: string(p)})
	}
	s.pickMenu.setItems(items)

	if editing != nil {
		s.name.SetValue(editing.Name)
		s.baseURL.SetValue(editing.BaseURL)
		s.mockURL.SetValue(editing.MockURL)
		s.rate.SetValue(strconv.Itoa(editing.RateLimitRPM))
		s.provider = editing.ProviderType
	} else if def := models.ProviderDefaultURL(s.provider); def != "" {
		s.baseURL.SetValue(def)
		s.mockURL.SetValue(def)
	}
	s.initialProvider = s.provider
	s.refreshActionLabels()
	return s
}

func (s *apiKeyFormScreen) refreshActionLabels() {
	s.form.setActionLabel("provider", "Provider: "+string(s.provider))
}

// applyDefaults fills empty base/mock URLs with the provider default.
func (s *apiKeyFormScreen) applyDefaults() {
	def := models.ProviderDefaultURL(s.provider)
	if def == "" {
		return
	}
	if strings.TrimSpace(s.baseURL.Value()) == "" {
		s.baseURL.SetValue(def)
	}
	if strings.TrimSpace(s.mockURL.Value()) == "" {
		s.mockURL.SetValue(def)
	}
}

func (s *apiKeyFormScreen) ID() string           { return "apikey-form" }
func (s *apiKeyFormScreen) Title() string        { return "API Key" }
func (s *apiKeyFormScreen) CapturingInput() bool { return s.form.Capturing() || s.picking }
func (s *apiKeyFormScreen) Init() tea.Cmd {
	if s.editing == nil || !s.editing.HasSecret {
		return nil
	}
	id := s.editing.ID
	return func() tea.Msg {
		secret, err := s.app.ctx.Client.GetAPIKeySecret(context.Background(), id)
		if err != nil {
			return nil
		}
		return apiKeySecretMsg{secret: secret}
	}
}

func (s *apiKeyFormScreen) dirty() bool {
	return s.form.Dirty() || s.provider != s.initialProvider
}

func (s *apiKeyFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case apiKeySavedMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, setBusy(false, "")
		}
		return s, tea.Batch(pop(), setStatus("saved key "+msg.key.Name, false), setBusy(false, ""))
	case apiKeySecretMsg:
		s.secret.SetValue(msg.secret)
		return s, nil
	case apiKeyTestMsg:
		return s, tea.Batch(setStatus(testStatus(msg.res, msg.err), !testOK(msg.res, msg.err)), setBusy(false, ""))
	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s *apiKeyFormScreen) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	if s.picking {
		if msg.String() == "esc" {
			s.picking = false
			return s, nil
		}
		if key, ok := s.pickMenu.update(msg); ok {
			s.provider = models.ProviderType(key)
			s.picking = false
			if def := models.ProviderDefaultURL(s.provider); def != "" {
				s.baseURL.SetValue(def)
				s.mockURL.SetValue(def)
			}
			s.refreshActionLabels()
		}
		return s, nil
	}
	if s.form.Capturing() {
		_, _, cmd := s.form.Update(msg)
		return s, cmd
	}
	switch msg.String() {
	case "esc":
		if s.dirty() {
			return s, confirm("Discard all unsaved changes and go back?", func() tea.Cmd { return pop() })
		}
		return s, pop()
	case "ctrl+s":
		return s, s.save()
	case "ctrl+t":
		return s, s.test()
	case "ctrl+d":
		return s, s.deleteKey()
	}
	action, handled, cmd := s.form.Update(msg)
	if handled && action != "" {
		switch action {
		case "save":
			return s, s.save()
		case "test":
			return s, s.test()
		case "delete":
			return s, s.deleteKey()
		case "provider":
			s.picking = true
		}
	}
	return s, cmd
}

// validateForTest validates only what a connection test needs (no name).
func (s *apiKeyFormScreen) validateForTest() error {
	s.applyDefaults()
	if err := validateURL(s.baseURL.Value()); err != nil {
		return errValidation("base URL: " + err.Error())
	}
	if s.secret.Value() == "" && s.editing == nil {
		return errValidation("secret is required")
	}
	return nil
}

func testOK(res apitypes.TestConnectionResponse, err error) bool {
	return err == nil && res.OK
}

func testStatus(res apitypes.TestConnectionResponse, err error) string {
	if err != nil {
		return "test failed: " + err.Error()
	}
	if res.OK {
		return "key valid (HTTP " + strconv.Itoa(res.StatusCode) + ")"
	}
	msg := res.Message
	if msg == "" {
		msg = res.Error
	}
	if msg == "" {
		msg = "test failed"
	}
	if res.StatusCode > 0 {
		return msg + " (HTTP " + strconv.Itoa(res.StatusCode) + ")"
	}
	return msg
}

func (s *apiKeyFormScreen) validate() error {
	s.applyDefaults()
	if strings.TrimSpace(s.name.Value()) == "" {
		return errValidation("name is required")
	}
	if err := validateURL(s.baseURL.Value()); err != nil {
		return errValidation("base URL: " + err.Error())
	}
	if err := validateURL(s.mockURL.Value()); err != nil {
		return errValidation("mock URL: " + err.Error())
	}
	if strings.TrimSpace(s.rate.Value()) != "" {
		if _, err := strconv.Atoi(strings.TrimSpace(s.rate.Value())); err != nil {
			return errValidation("rate limit must be an integer")
		}
	}
	if s.secret.Value() == "" && (s.editing == nil || !s.editing.HasSecret) {
		return errValidation("secret is required")
	}
	return nil
}

func (s *apiKeyFormScreen) payload() apitypes.APIKeyPayload {
	rate, _ := strconv.Atoi(strings.TrimSpace(s.rate.Value()))
	return apitypes.APIKeyPayload{
		APIKey: models.APIKey{
			Name:         strings.TrimSpace(s.name.Value()),
			ProviderType: s.provider,
			BaseURL:      strings.TrimSpace(s.baseURL.Value()),
			MockURL:      strings.TrimSpace(s.mockURL.Value()),
			RateLimitRPM: rate,
		},
		Secret: s.secret.Value(),
	}
}

func (s *apiKeyFormScreen) save() tea.Cmd {
	if err := s.validate(); err != nil {
		s.err = err.Error()
		return nil
	}
	s.err = ""
	payload := s.payload()
	editing := s.editing
	return tea.Batch(setBusy(true, "Saving key"), func() tea.Msg {
		ctx := context.Background()
		if editing == nil {
			key, err := s.app.ctx.Client.CreateAPIKey(ctx, payload)
			return apiKeySavedMsg{key: key, err: err}
		}
		key, err := s.app.ctx.Client.UpdateAPIKey(ctx, editing.ID, payload)
		return apiKeySavedMsg{key: key, err: err}
	})
}

func (s *apiKeyFormScreen) test() tea.Cmd {
	if err := s.validateForTest(); err != nil {
		s.err = err.Error()
		return nil
	}
	s.err = ""
	req := apitypes.TestConnectionRequest{
		ProviderType: s.provider,
		BaseURL:      strings.TrimSpace(s.baseURL.Value()),
		Secret:       s.secret.Value(),
	}
	if req.Secret == "" && s.editing != nil {
		req.KeyID = s.editing.ID
	}
	return tea.Batch(setBusy(true, "Testing connection"), func() tea.Msg {
		res, err := s.app.ctx.Client.TestAPIKeyPayload(context.Background(), req)
		return apiKeyTestMsg{res: res, err: err}
	})
}

func (s *apiKeyFormScreen) deleteKey() tea.Cmd {
	if s.editing == nil {
		return nil
	}
	id := s.editing.ID
	return confirm("Delete API key "+s.editing.Name+"?", func() tea.Cmd {
		return func() tea.Msg {
			err := s.app.ctx.Client.DeleteAPIKey(context.Background(), id)
			return apiKeysActionMsg{status: "key deleted", err: err}
		}
	})
}

func (s *apiKeyFormScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render("API Key") + "\n\n")
	if s.picking {
		b.WriteString("Provider type:\n\n" + s.pickMenu.view())
		return b.String()
	}
	s.form.setTop(2)
	b.WriteString(s.form.view() + "\n")
	if s.editing != nil && !s.editing.HasSecret {
		b.WriteString("\n" + t.Error.Render("No secret is stored for this key — enter one and save.") + "\n")
	}
	if s.err != "" {
		b.WriteString("\n" + t.Error.Render(s.err) + "\n")
	}
	b.WriteString("\n" + t.Dim.Render("Enter edit/activate · ↑↓ move · Esc back"))
	return b.String()
}

// errValidation marks a user-facing validation error.
type errValidation string

func (e errValidation) Error() string { return string(e) }

func validateURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errValidation("is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errValidation("must be an absolute URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errValidation("must use http or https")
	}
	return nil
}
