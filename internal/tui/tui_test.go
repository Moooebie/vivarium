package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"vivarium/internal/apitypes"
	"vivarium/internal/client"
	"vivarium/internal/models"
)

// The production client must satisfy the TUI's API interface.
var _ API = (*client.Client)(nil)

// fakeAPI embeds the interface so only the methods a test needs are overridden.
type fakeAPI struct {
	API
	createRecipeCalls int
}

func (f *fakeAPI) CreateRecipe(_ context.Context, r models.Recipe) (models.Recipe, error) {
	f.createRecipeCalls++
	return r, nil
}

type stubScreen struct{ id string }

func (s stubScreen) ID() string                       { return s.id }
func (s stubScreen) Title() string                    { return s.id }
func (s stubScreen) Init() tea.Cmd                    { return nil }
func (s stubScreen) Update(tea.Msg) (Screen, tea.Cmd) { return s, nil }
func (s stubScreen) View() string                     { return s.id }

func TestAppNavigationStack(t *testing.T) {
	app := newApp(&Context{Version: "test"})
	if len(app.stack) != 1 {
		t.Fatalf("initial stack = %d", len(app.stack))
	}
	model, _ := app.Update(pushMsg{screen: stubScreen{id: "child"}})
	app = model.(*App)
	if len(app.stack) != 2 || app.top().ID() != "child" {
		t.Fatalf("after push: len=%d top=%s", len(app.stack), app.top().ID())
	}
	model, _ = app.Update(popMsg{})
	app = model.(*App)
	if len(app.stack) != 1 {
		t.Fatalf("after pop: len=%d", len(app.stack))
	}
	// Pop must not remove the root.
	model, _ = app.Update(popMsg{})
	app = model.(*App)
	if len(app.stack) != 1 {
		t.Fatalf("root popped: len=%d", len(app.stack))
	}
}

func TestQuitFromRoot(t *testing.T) {
	app := newApp(&Context{Version: "test"})
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("expected a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg, got %T", cmd())
	}
}

func TestQuitConfirmsOnSubpage(t *testing.T) {
	app := newApp(&Context{Version: "test"})
	model, _ := app.Update(pushMsg{screen: stubScreen{id: "child"}})
	app = model.(*App)
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("expected a confirmation command")
	}
	model, _ = app.Update(cmd()) // process confirmMsg
	app = model.(*App)
	if app.confirm == nil {
		t.Fatal("expected a confirmation modal on a sub-page")
	}
	// Answering n dismisses the modal.
	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	app = model.(*App)
	if app.confirm != nil {
		t.Fatal("modal not dismissed")
	}
}

func TestPopReloadsParent(t *testing.T) {
	app := newApp(&Context{Client: &fakeAPI{}, Version: "test"})
	model, _ := app.Update(pushMsg{screen: newInstancesScreen(app)})
	app = model.(*App)
	model, _ = app.Update(pushMsg{screen: stubScreen{id: "child"}})
	app = model.(*App)
	if _, ok := app.top().(reloader); ok {
		t.Fatal("child should not be a reloader")
	}
	_, cmd := app.Update(popMsg{})
	if cmd == nil {
		t.Fatal("expected the parent list to reload on pop")
	}
}

func TestMenuSkipsHeaders(t *testing.T) {
	m := newMenu(DefaultTheme())
	m.setItems([]menuItem{
		{Header: true, Label: "SECTION"},
		{Key: "a", Label: "A"},
		{Key: "b", Label: "B"},
	})
	// The cursor starts on the first selectable row, skipping the header.
	if key, _ := m.selected(); key != "a" {
		t.Fatalf("initial selectable = %q", key)
	}
	m.move(1)
	if key, _ := m.selected(); key != "b" {
		t.Fatalf("second selectable = %q", key)
	}
	m.move(1)
	if key, _ := m.selected(); key != "b" {
		t.Fatalf("should clamp at last, got %q", key)
	}
	// Enter returns the selected key.
	if key, ok := m.update(tea.KeyMsg{Type: tea.KeyEnter}); !ok || key != "b" {
		t.Fatalf("enter = %q, %v", key, ok)
	}
}

func TestInstancesOrdering(t *testing.T) {
	app := &App{ctx: &Context{Cwd: "/pinned"}, theme: DefaultTheme()}
	s := newInstancesScreen(app)
	s.setViews([]apitypes.InstanceView{
		{Instance: models.Instance{ID: "h", Name: "zeta", Status: models.StatusHalted, BaseImageTag: "img"}},
		{Instance: models.Instance{ID: "r", Name: "alpha", Status: models.StatusRunning, BaseImageTag: "img"}},
		{Instance: models.Instance{ID: "p", Name: "pinned", Status: models.StatusHalted, BaseImageTag: "img",
			Mounts: []models.Mount{{HostPath: "/pinned", GuestPath: "/mnt/x", Mode: models.MountReadWrite}}}},
	})
	var keys []string
	for _, it := range s.menu.items {
		if !it.Header {
			keys = append(keys, it.Key)
		}
	}
	want := []string{"instance:p", "instance:r", "instance:h"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", keys, want)
	}
}

func TestExpandPath(t *testing.T) {
	abs := expandPath("/tmp/foo")
	if abs != "/tmp/foo" {
		t.Fatalf("abs = %q", abs)
	}
	home, _ := os.UserHomeDir()
	got := expandPath("~/thing")
	if got != filepath.Join(home, "thing") {
		t.Fatalf("tilde = %q", got)
	}
	cwd, _ := os.Getwd()
	got = expandPath("relative")
	if got != filepath.Join(cwd, "relative") {
		t.Fatalf("relative = %q", got)
	}
}

func TestEnvTextRoundTrip(t *testing.T) {
	env := map[string]string{"B": "2", "A": "1"}
	text := envToText(env)
	if text != "A=1\nB=2" {
		t.Fatalf("envToText = %q", text)
	}
	got := textToEnv("A=1\n# comment\nB=2\n\n")
	if got["A"] != "1" || got["B"] != "2" || len(got) != 2 {
		t.Fatalf("textToEnv = %v", got)
	}
}

func TestFormVerticalRows(t *testing.T) {
	f := newForm(DefaultTheme())
	name := newField(DefaultTheme(), "Name", "", false)
	f.addField(name)
	f.addAction("open", "Open")
	f.addButton(button{Key: "save", Label: "Save"})

	// Enter on the focused field begins editing.
	if _, handled, _ := f.Update(tea.KeyMsg{Type: tea.KeyEnter}); !handled || !f.Capturing() {
		t.Fatal("expected edit mode")
	}
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if f.Capturing() || name.Value() != "hello" {
		t.Fatalf("commit failed: capturing=%v value=%q", f.Capturing(), name.Value())
	}

	// Moving down reaches the action row; Enter returns its key.
	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if action, handled, _ := f.Update(tea.KeyMsg{Type: tea.KeyEnter}); !handled || action != "open" {
		t.Fatalf("action = %q handled=%v", action, handled)
	}
	// Moving down again reaches the button row.
	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if action, handled, _ := f.Update(tea.KeyMsg{Type: tea.KeyEnter}); !handled || action != "save" {
		t.Fatalf("button = %q handled=%v", action, handled)
	}
}

func TestFormEditCancelReverts(t *testing.T) {
	f := newForm(DefaultTheme())
	name := newField(DefaultTheme(), "Name", "", false)
	f.addField(name)
	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if name.Value() != "" {
		t.Fatalf("cancel did not revert: %q", name.Value())
	}
}

func TestMenuDoubleClickDrills(t *testing.T) {
	m := newMenu(DefaultTheme())
	m.setItems([]menuItem{{Key: "a", Label: "A"}, {Key: "b", Label: "B"}})
	m.setTop(0)
	click := tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 0, Y: 1}
	if key, ok := m.update(click); ok {
		t.Fatalf("single click should only select, got %q", key)
	}
	if key, ok := m.update(click); !ok || key != "b" {
		t.Fatalf("double click = %q, %v", key, ok)
	}
}

func TestCustomizerEphemeralDoesNotPersist(t *testing.T) {
	fake := &fakeAPI{}
	app := &App{ctx: &Context{Client: fake}, theme: DefaultTheme(), spinner: spinner.New()}
	target := models.Recipe{Name: "orig", EnvVars: map[string]string{}}
	ed := newRecipeEditorScreen(app, target, &target)
	ed.name.input.SetValue("edited")
	cmd := ed.finishEphemeral()
	if cmd == nil {
		t.Fatal("expected a completion command")
	}
	if _, ok := cmd().(customizeDoneMsg); !ok {
		t.Fatalf("expected customizeDoneMsg, got %T", cmd())
	}
	if target.Name != "edited" {
		t.Fatalf("recipe not updated in place: %q", target.Name)
	}
	if fake.createRecipeCalls != 0 {
		t.Fatalf("ephemeral customizer persisted %d recipes", fake.createRecipeCalls)
	}
	_ = time.Now
}

func TestScreenViewsRender(t *testing.T) {
	app := &App{ctx: &Context{Client: &fakeAPI{}, Cwd: "/work", Version: "t"}, theme: DefaultTheme(), spinner: spinner.New()}
	inst := apitypes.InstanceView{Instance: models.Instance{
		ID: "i1", Name: "dev", Status: models.StatusRunning, BaseImageTag: "img",
		Mounts: []models.Mount{{HostPath: "/work", GuestPath: "/mnt/w", Mode: models.MountReadWrite}},
	}}
	screens := []Screen{
		newAPIKeyFormScreen(app, nil),
		newBaseImageFormScreen(app),
		newEditInstanceScreen(app, inst),
		newWizardScreen(app),
		newConnectionInfoScreen(app, inst),
	}
	for _, s := range screens {
		if s.View() == "" {
			t.Fatalf("%s rendered empty view", s.ID())
		}
	}

	ed := newRecipeEditorScreen(app, models.Recipe{EnvVars: map[string]string{}}, nil)
	ed.Update(recipeRefDataMsg{
		images: []apitypes.BaseImageView{{BaseImage: models.BaseImage{ID: "b1", Name: "Ubuntu"}}},
		keys:   []apitypes.APIKeyView{{APIKey: models.APIKey{ID: "k1", Name: "Key", MockURL: "https://api.openai.com/v1"}, HasSecret: true}},
		gpus:   []models.GPU{{DeviceID: "0000:03:00.0", Name: "AMD", RenderNode: "/dev/dri/renderD128"}},
	})
	if ed.View() == "" {
		t.Fatal("recipe editor rendered empty view")
	}
}

func TestWizardRequiresName(t *testing.T) {
	app := &App{ctx: &Context{Client: &fakeAPI{}}, theme: DefaultTheme(), spinner: spinner.New()}
	w := newWizardScreen(app)
	if _, cmd := w.Update(tea.KeyMsg{Type: tea.KeyCtrlN}); cmd != nil {
		t.Fatal("Next should be blocked when the name is empty")
	}
	if w.err == "" {
		t.Fatal("expected a name-required error")
	}
}

func TestReadLogoFallsBackToEmbedded(t *testing.T) {
	// The test working directory has no ./LOGO, so the embedded copy is used.
	if strings.TrimSpace(readLogo()) == "" {
		t.Fatal("expected a logo (embedded fallback)")
	}
}

func TestAPIKeyFormProviderSetsDefaults(t *testing.T) {
	app := &App{ctx: &Context{Client: &fakeAPI{}}, theme: DefaultTheme(), spinner: spinner.New()}
	f := newAPIKeyFormScreen(app, nil)

	// Move to the Provider action row and open the picker.
	for i := 0; i < 5; i++ {
		f.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// Select deepseek (index 2).
	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	f.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if f.provider != models.ProviderDeepSeek {
		t.Fatalf("provider = %q", f.provider)
	}
	if f.baseURL.Value() != "https://api.deepseek.com/v1" {
		t.Fatalf("base URL = %q", f.baseURL.Value())
	}
	if f.mockURL.Value() != "https://api.deepseek.com/v1" {
		t.Fatalf("mock URL = %q", f.mockURL.Value())
	}
}

func TestRecipeAddEndpoint(t *testing.T) {
	app := &App{ctx: &Context{Client: &fakeAPI{}}, theme: DefaultTheme(), spinner: spinner.New()}
	ed := newRecipeEditorScreen(app, models.Recipe{EnvVars: map[string]string{}}, nil)
	host := recipeEndpointsHost{ed}

	openai := models.APIKey{
		ID: "k1", Name: "OpenAI", ProviderType: models.ProviderOpenAI,
		BaseURL: "https://api.openai.com/v1", MockURL: "https://api.openai.com/v1",
	}
	if err := host.AddEndpoint(openai); err != nil {
		t.Fatal(err)
	}
	if len(ed.recipe.APIEndpoints) != 1 || ed.recipe.APIEndpoints[0].ID != "k1" {
		t.Fatalf("endpoints = %+v", ed.recipe.APIEndpoints)
	}
	// A duplicate mock URL is rejected.
	if err := host.AddEndpoint(openai); err == nil {
		t.Fatal("expected a duplicate mock_url error")
	}
	// Endpoints no longer add provider env vars; those are injected per exec.
	custom := models.APIKey{ID: "k2", ProviderType: models.ProviderCustom, BaseURL: "https://x/v1", MockURL: "https://x/v1"}
	if err := host.AddEndpoint(custom); err != nil {
		t.Fatal(err)
	}
	if len(ed.recipe.EnvVars) != 0 {
		t.Fatalf("endpoints must not add recipe env vars, got %+v", ed.recipe.EnvVars)
	}
}

func TestConnectionInfoScreen(t *testing.T) {
	app := &App{ctx: &Context{Client: &fakeAPI{}}, theme: DefaultTheme(), spinner: spinner.New()}
	inst := apitypes.InstanceView{Instance: models.Instance{
		ID: "i1", Name: "dev", Status: models.StatusRunning, BaseImageTag: "img",
		Endpoints: []models.InstanceEndpoint{
			{KeyID: "k1", ProviderType: models.ProviderDeepSeek,
				MockURL: "https://api.deepseek.com/v1", Token: "viv-tok-abc"},
		},
	}}
	view := newConnectionInfoScreen(app, inst).View()
	if !strings.Contains(view, "https://api.deepseek.com/v1") || !strings.Contains(view, "viv-tok-abc") {
		t.Fatalf("connection info missing details: %s", view)
	}
}

func TestInstancesOpenConnectionInfo(t *testing.T) {
	app := newApp(&Context{Client: &fakeAPI{}, Cwd: "/w", Version: "t"})
	inst := newInstancesScreen(app)
	inst.setViews([]apitypes.InstanceView{{Instance: models.Instance{
		ID: "i1", Name: "dev", Status: models.StatusRunning, BaseImageTag: "img",
	}}})
	model, _ := app.Update(pushMsg{screen: inst})
	app = model.(*App)

	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if cmd == nil {
		t.Fatal("expected a navigation command")
	}
	model, _ = app.Update(cmd())
	app = model.(*App)
	if app.top().ID() != "connection-info" {
		t.Fatalf("top = %s", app.top().ID())
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := humanBytes(4096); got != "4.0 KB" {
		t.Fatalf("humanBytes = %q", got)
	}
	if got := formatAgo(0); got != "never" {
		t.Fatalf("formatAgo(0) = %q", got)
	}
}

func TestAPIKeySecretStateBadges(t *testing.T) {
	app := &App{ctx: &Context{Client: &fakeAPI{}}, theme: DefaultTheme(), spinner: spinner.New()}
	s := newAPIKeysScreen(app)
	s.Update(apiKeysLoadedMsg{keys: []apitypes.APIKeyView{
		{APIKey: models.APIKey{ID: "ok", Name: "Ok"}, SecretState: apitypes.SecretOK},
		{APIKey: models.APIKey{ID: "missing", Name: "Missing"}, SecretState: apitypes.SecretMissing},
		{APIKey: models.APIKey{ID: "locked", Name: "Locked"}, SecretState: apitypes.SecretLocked},
		{APIKey: models.APIKey{ID: "err", Name: "Err"}, SecretState: apitypes.SecretError, SecretError: "boom"},
	}})

	badges := map[string]string{}
	for _, it := range s.menu.items {
		badges[it.Label] = it.Badge
	}
	if badges["Ok"] != "" {
		t.Fatalf("ok badge = %q, want none", badges["Ok"])
	}
	if badges["Missing"] != "MISSING" {
		t.Fatalf("missing badge = %q", badges["Missing"])
	}
	if badges["Locked"] != "LOCKED" {
		t.Fatalf("locked badge = %q", badges["Locked"])
	}
	if badges["Err"] != "SECRET ERROR" {
		t.Fatalf("error badge = %q", badges["Err"])
	}

	// Older backends that only set HasSecret are still classified.
	if got := secretStateOf(apitypes.APIKeyView{HasSecret: true}); got != apitypes.SecretOK {
		t.Fatalf("legacy has-secret state = %q", got)
	}
	if got := secretStateOf(apitypes.APIKeyView{}); got != apitypes.SecretMissing {
		t.Fatalf("legacy no-secret state = %q", got)
	}
}

func TestInstanceEditorEnvAction(t *testing.T) {
	app := &App{ctx: &Context{Client: &fakeAPI{}}, theme: DefaultTheme(), spinner: spinner.New()}
	s := newEditInstanceScreen(app, apitypes.InstanceView{Instance: models.Instance{
		ID: "i1", Name: "dev", Status: models.StatusRunning, BaseImageTag: "img",
		EnvVars: map[string]string{"A": "1", "B": "2"},
	}})
	cmd := s.dispatch("env")
	if cmd == nil {
		t.Fatal("expected the env editor to be pushed")
	}
	if _, ok := cmd().(pushMsg); !ok {
		t.Fatalf("expected pushMsg, got %T", cmd())
	}
	// Mounts/GPUs are fixed at creation and are no longer dispatchable.
	if cmd := s.dispatch("mounts"); cmd != nil {
		t.Fatal("mounts should not be editable")
	}
	if cmd := s.dispatch("gpus"); cmd != nil {
		t.Fatal("gpus should not be editable")
	}
}

func TestCloneRequestCarriesEnv(t *testing.T) {
	v := apitypes.InstanceView{Instance: models.Instance{
		Name: "dev", BaseImageTag: "img", EnvVars: map[string]string{"A": "1"},
	}}
	req := cloneRequest(v)
	if req.Recipe == nil || req.Recipe.EnvVars["A"] != "1" {
		t.Fatalf("clone env = %+v", req.Recipe)
	}
}
