package tui

import (
	"context"
	"errors"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

type recipesScreen struct {
	app     *App
	menu    *menu
	recipes []models.Recipe
	err     string
}

type recipesLoadedMsg struct {
	recipes []models.Recipe
	err     error
}

type recipesActionMsg struct {
	status string
	err    error
}

func newRecipesScreen(app *App) *recipesScreen {
	return &recipesScreen{app: app, menu: newMenu(app.theme)}
}

func (s *recipesScreen) ID() string    { return "recipes" }
func (s *recipesScreen) Title() string { return "Recipes" }
func (s *recipesScreen) Init() tea.Cmd { return s.load() }

// Reload implements reloader.
func (s *recipesScreen) Reload() tea.Cmd { return s.load() }

func (s *recipesScreen) load() tea.Cmd {
	return func() tea.Msg {
		recipes, err := s.app.ctx.Client.ListRecipes(context.Background())
		return recipesLoadedMsg{recipes: recipes, err: err}
	}
}

func (s *recipesScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case recipesLoadedMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, nil
		}
		s.err = ""
		sort.Slice(msg.recipes, func(i, j int) bool { return msg.recipes[i].Name < msg.recipes[j].Name })
		s.recipes = msg.recipes
		items := make([]menuItem, 0, len(msg.recipes))
		for _, r := range msg.recipes {
			items = append(items, menuItem{
				Key:   "recipe:" + r.ID,
				Label: r.Name,
				Detail: itoa(len(r.APIEndpoints)) + " endpoints · " +
					itoa(len(r.EnvVars)) + " env · " + itoa(len(r.DefaultMounts)) + " mounts",
			})
		}
		if len(items) == 0 {
			items = append(items, menuItem{Header: true, Label: "NO RECIPES — press n to create one"})
		}
		s.menu.setItems(items)
		s.menu.setSize(s.app.width, maxInt(1, s.app.height-6))
		return s, nil
	case recipesActionMsg:
		cmd := s.load()
		if msg.err != nil {
			return s, tea.Batch(cmd, setStatus(msg.status+": "+msg.err.Error(), true), setBusy(false, ""))
		}
		return s, tea.Batch(cmd, setStatus(msg.status, false), setBusy(false, ""))
	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s *recipesScreen) selected() (models.Recipe, bool) {
	key, ok := s.menu.selected()
	if !ok || !strings.HasPrefix(key, "recipe:") {
		return models.Recipe{}, false
	}
	id := strings.TrimPrefix(key, "recipe:")
	for _, r := range s.recipes {
		if r.ID == id {
			return r, true
		}
	}
	return models.Recipe{}, false
}

func (s *recipesScreen) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return s, pop()
	case "r":
		return s, s.load()
	case "n":
		return s, push(newRecipeEditorScreen(s.app, models.Recipe{EnvVars: map[string]string{}}, nil))
	case "e", "enter":
		if r, ok := s.selected(); ok {
			return s, push(newRecipeEditorScreen(s.app, r, nil))
		}
	case "c":
		if r, ok := s.selected(); ok {
			r.ID = ""
			r.Name = r.Name + " copy"
			return s, push(newRecipeEditorScreen(s.app, r, nil))
		}
	case "d":
		if r, ok := s.selected(); ok {
			id, name := r.ID, r.Name
			return s, confirm("Delete recipe "+name+"?", func() tea.Cmd { return s.deleteRecipe(id) })
		}
	}
	if _, ok := s.menu.update(msg); ok {
		if r, ok := s.selected(); ok {
			return s, push(newRecipeEditorScreen(s.app, r, nil))
		}
	}
	return s, nil
}

func (s *recipesScreen) deleteRecipe(id string) tea.Cmd {
	return func() tea.Msg {
		err := s.app.ctx.Client.DeleteRecipe(context.Background(), id)
		return recipesActionMsg{status: "recipe deleted", err: err}
	}
}

func (s *recipesScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render("Recipes") + "\n\n")
	if s.err != "" {
		b.WriteString(t.Error.Render(s.err) + "\n\n")
	}
	s.menu.setTop(2)
	b.WriteString(s.menu.view())
	b.WriteString("\n\n" + s.app.helpBar("n new", "e edit", "c clone", "d delete", "r refresh", "Esc back"))
	return b.String()
}

// ---- Recipe editor / customizer ----

// customizeDoneMsg is emitted by an ephemeral editor and handled by App, which
// pops the editor and forwards the message to the parent screen.
type customizeDoneMsg struct{}

type recipeEditorScreen struct {
	app       *App
	recipe    models.Recipe
	ephemeral bool
	recipePtr *models.Recipe

	form    *form
	name    *field
	images  []apitypes.BaseImageView
	keys    []apitypes.APIKeyView
	gpus    []models.GPU
	changed bool
	err     string
}

type recipeSavedMsg struct {
	recipe models.Recipe
	err    error
}

type recipeRefDataMsg struct {
	images []apitypes.BaseImageView
	keys   []apitypes.APIKeyView
	gpus   []models.GPU
	err    error
}

func newRecipeEditorScreen(app *App, r models.Recipe, recipePtr *models.Recipe) *recipeEditorScreen {
	s := &recipeEditorScreen{
		app:       app,
		recipe:    r,
		recipePtr: recipePtr,
		ephemeral: recipePtr != nil,
	}
	if s.recipe.EnvVars == nil {
		s.recipe.EnvVars = map[string]string{}
	}
	s.form = newForm(app.theme)
	s.name = newField(app.theme, "Name", "Input a name…", false)
	s.name.SetValue(r.Name)
	s.form.addField(s.name)
	s.form.addAction("base", "Base Image")
	s.form.addAction("env", "Environment Variables")
	s.form.addAction("endpoints", "API Endpoints")
	s.form.addAction("mounts", "Mount Points")
	s.form.addAction("resources", "Resources")
	s.form.addAction("gpus", "GPUs")
	if s.ephemeral {
		s.form.addButton(
			button{Key: "back", Label: "< Back", Hotkey: "Esc"},
			button{Key: "create", Label: "Create & Launch", Hotkey: "C-n"},
		)
	} else {
		s.form.addButton(
			button{Key: "save", Label: "Save", Hotkey: "C-s"},
			button{Key: "delete", Label: "Delete", Hotkey: "C-d"},
		)
	}
	s.refreshActionLabels()
	return s
}

func (s *recipeEditorScreen) ID() string           { return "recipe-editor" }
func (s *recipeEditorScreen) Title() string        { return "Recipe" }
func (s *recipeEditorScreen) CapturingInput() bool { return s.form.Capturing() }

func (s *recipeEditorScreen) Init() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		images, err := s.app.ctx.Client.ListBaseImages(ctx)
		if err != nil {
			return recipeRefDataMsg{err: err}
		}
		keys, _ := s.app.ctx.Client.ListAPIKeys(ctx)
		gpus, _ := s.app.ctx.Client.ListGPUs(ctx)
		return recipeRefDataMsg{images: images, keys: keys, gpus: gpus}
	}
}

func (s *recipeEditorScreen) refreshActionLabels() {
	s.form.setActionLabel("base", "Base Image: "+s.baseImageName())
	s.form.setActionLabel("env", "Environment Variables: "+itoa(len(s.recipe.EnvVars)))
	s.form.setActionLabel("endpoints", "API Endpoints: "+itoa(len(s.recipe.APIEndpoints)))
	s.form.setActionLabel("mounts", "Mount Points: "+itoa(len(s.recipe.DefaultMounts)))
	s.form.setActionLabel("resources", "Resources: "+itoa(len(s.recipe.Resources)))
	s.form.setActionLabel("gpus", "GPUs: "+itoa(len(s.recipe.GPUs)))
}

func (s *recipeEditorScreen) dirty() bool { return s.form.Dirty() || s.changed }

func (s *recipeEditorScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case recipeRefDataMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, nil
		}
		s.images, s.keys, s.gpus = msg.images, msg.keys, msg.gpus
		s.refreshActionLabels()
		return s, nil
	case recipeSavedMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, setBusy(false, "")
		}
		return s, tea.Batch(pop(), setStatus("saved recipe "+msg.recipe.Name, false), setBusy(false, ""))
	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s *recipeEditorScreen) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	if s.form.Capturing() {
		_, _, cmd := s.form.Update(msg)
		return s, cmd
	}
	switch msg.String() {
	case "esc":
		if s.ephemeral {
			return s, pop()
		}
		if s.dirty() {
			return s, confirm("Discard all unsaved changes and go back?", func() tea.Cmd { return pop() })
		}
		return s, pop()
	case "ctrl+s":
		if !s.ephemeral {
			return s, s.save()
		}
	case "ctrl+n":
		if s.ephemeral {
			return s, s.finishEphemeral()
		}
	case "ctrl+d":
		if !s.ephemeral && s.recipe.ID != "" {
			return s, s.deleteRecipe()
		}
	}
	action, handled, cmd := s.form.Update(msg)
	if handled && action != "" {
		return s, s.dispatch(action)
	}
	return s, cmd
}

func (s *recipeEditorScreen) dispatch(action string) tea.Cmd {
	switch action {
	case "base":
		return push(newBaseImagePickerScreen(s.app, s.images, func(id string) {
			s.recipe.BaseImageID = id
			s.changed = true
			s.refreshActionLabels()
		}))
	case "env":
		return push(newEnvVarsEditorScreen(s.app, s.recipe.EnvVars, func(env map[string]string) tea.Cmd {
			s.recipe.EnvVars = env
			s.changed = true
			s.refreshActionLabels()
			return nil
		}))
	case "endpoints":
		return push(newEndpointsEditorScreen(s.app, recipeEndpointsHost{s}, nil, nil))
	case "mounts":
		return push(newMountsEditorScreen(s.app, "Mount Points", s.recipe.DefaultMounts, func(ms []models.Mount) tea.Cmd {
			s.recipe.DefaultMounts = ms
			s.changed = true
			s.refreshActionLabels()
			return nil
		}))
	case "resources":
		return push(newResourcesEditorScreen(s.app, "Resources", s.recipe.Resources, func(rs []models.Resource) tea.Cmd {
			s.recipe.Resources = rs
			s.changed = true
			s.refreshActionLabels()
			return nil
		}))
	case "gpus":
		return push(newGPUsEditorScreen(s.app, "GPUs", s.gpus, s.recipe.GPUs, func(gs []models.GPU) tea.Cmd {
			s.recipe.GPUs = gs
			s.changed = true
			s.refreshActionLabels()
			return nil
		}))
	case "save":
		return s.save()
	case "delete":
		return s.deleteRecipe()
	case "back":
		return pop()
	case "create":
		return s.finishEphemeral()
	}
	return nil
}

func (s *recipeEditorScreen) baseImageName() string {
	for _, img := range s.images {
		if img.ID == s.recipe.BaseImageID {
			return img.Name
		}
	}
	if s.recipe.BaseImageID == "" {
		return "(none)"
	}
	return s.recipe.BaseImageID
}

// recipeEndpointsHost adapts a recipe being edited in memory to the shared
// endpoints editor. Persistence happens when the recipe is saved.
type recipeEndpointsHost struct{ ed *recipeEditorScreen }

func (h recipeEndpointsHost) Endpoints() []models.APIKey { return h.ed.recipe.APIEndpoints }
func (h recipeEndpointsHost) SetEndpoints(eps []models.APIKey) {
	h.ed.recipe.APIEndpoints = eps
}
func (h recipeEndpointsHost) SavedKeys() []apitypes.APIKeyView { return h.ed.keys }
func (h recipeEndpointsHost) SetSavedKeys(keys []apitypes.APIKeyView) {
	h.ed.keys = keys
}
func (h recipeEndpointsHost) AddEndpoint(k models.APIKey) error {
	for _, e := range h.ed.recipe.APIEndpoints {
		if e.MockURL == k.MockURL {
			return errors.New("an endpoint with that mock_url already exists")
		}
	}
	h.ed.recipe.APIEndpoints = append(h.ed.recipe.APIEndpoints, k)
	return nil
}
func (h recipeEndpointsHost) OnChanged() {
	h.ed.changed = true
	h.ed.refreshActionLabels()
}

func (s *recipeEditorScreen) commit() {
	s.recipe.Name = strings.TrimSpace(s.name.Value())
}

func (s *recipeEditorScreen) save() tea.Cmd {
	s.commit()
	if strings.TrimSpace(s.recipe.Name) == "" {
		s.err = "name is required"
		return nil
	}
	s.err = ""
	r := s.recipe
	return tea.Batch(setBusy(true, "Saving recipe"), func() tea.Msg {
		ctx := context.Background()
		if r.ID == "" {
			out, err := s.app.ctx.Client.CreateRecipe(ctx, r)
			return recipeSavedMsg{recipe: out, err: err}
		}
		out, err := s.app.ctx.Client.UpdateRecipe(ctx, r.ID, r)
		return recipeSavedMsg{recipe: out, err: err}
	})
}

func (s *recipeEditorScreen) deleteRecipe() tea.Cmd {
	id := s.recipe.ID
	return confirm("Delete this recipe?", func() tea.Cmd {
		return func() tea.Msg {
			err := s.app.ctx.Client.DeleteRecipe(context.Background(), id)
			return recipesActionMsg{status: "recipe deleted", err: err}
		}
	})
}

func (s *recipeEditorScreen) finishEphemeral() tea.Cmd {
	s.commit()
	if s.recipePtr != nil {
		*s.recipePtr = s.recipe
	}
	return func() tea.Msg { return customizeDoneMsg{} }
}

func (s *recipeEditorScreen) View() string {
	t := s.app.theme
	title := "Recipe Editor"
	if s.ephemeral {
		title = "Configure Instance"
	}
	var b strings.Builder
	b.WriteString(t.Title.Render(title) + "\n\n")
	if s.err != "" {
		b.WriteString(t.Error.Render(s.err) + "\n\n")
	}
	s.form.setTop(2)
	b.WriteString(s.form.view())
	b.WriteString("\n\n" + s.app.helpBar("Enter edit/open", "↑↓ move", "Esc back"))
	return b.String()
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 && i < len(p)-1 {
		return p[i+1:]
	}
	return p
}

func envToText(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + env[k] + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func textToEnv(text string) map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			env[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return env
}
