package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

// wizardScreen is the first step of instance creation: identity and recipe
// selection. Choosing "Next" opens the ephemeral recipe editor, whose
// "Create & Launch" button provisions the instance.
type wizardScreen struct {
	app     *App
	form    *form
	name    *field
	menu    *menu
	picking bool
	recipes []models.Recipe
	recipe  *models.Recipe
	images  []apitypes.BaseImageView
	gpus    []models.GPU
	err     string
}

type wizardRefMsg struct {
	recipes []models.Recipe
	gpus    []models.GPU
	images  []apitypes.BaseImageView
	err     error
}

type wizardCreatedMsg struct {
	inst apitypes.InstanceView
	err  error
}

func newWizardScreen(app *App) *wizardScreen {
	s := &wizardScreen{
		app:    app,
		menu:   newMenu(app.theme),
		recipe: &models.Recipe{EnvVars: map[string]string{}},
	}
	s.form = newForm(app.theme)
	s.name = newField(app.theme, "Instance Name", "Input a name…", false)
	s.form.addField(s.name)
	s.form.addAction("recipe", "Recipe")
	s.form.addButton(button{Key: "next", Label: "Next >", Hotkey: "C-n"})
	s.refreshActionLabels()
	return s
}

func (s *wizardScreen) ID() string    { return "wizard" }
func (s *wizardScreen) Title() string { return "New Instance" }

func (s *wizardScreen) CapturingInput() bool { return s.form.Capturing() || s.picking }

func (s *wizardScreen) Init() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		recipes, err := s.app.ctx.Client.ListRecipes(ctx)
		if err != nil {
			return wizardRefMsg{err: err}
		}
		gpus, _ := s.app.ctx.Client.ListGPUs(ctx)
		images, _ := s.app.ctx.Client.ListBaseImages(ctx)
		return wizardRefMsg{recipes: recipes, gpus: gpus, images: images}
	}
}

func (s *wizardScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case wizardRefMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, nil
		}
		s.recipes, s.gpus, s.images = msg.recipes, msg.gpus, msg.images
		s.buildRecipeMenu()
		return s, nil
	case customizeDoneMsg:
		// The ephemeral editor committed; provision the instance.
		return s, s.create()
	case wizardCreatedMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, setBusy(false, "")
		}
		connect := &connectCmd{client: s.app.ctx.Client, id: msg.inst.ID, ctx: context.Background()}
		return s, tea.Batch(
			setBusy(false, ""),
			resetTo(newInstancesScreen(s.app)),
			tea.Exec(connect, func(err error) tea.Msg { return connectDoneMsg{err: err} }),
		)
	case tea.KeyMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s *wizardScreen) buildRecipeMenu() {
	items := []menuItem{{Key: "__none__", Label: "(none)", Detail: "blank configuration"}}
	for _, r := range s.recipes {
		items = append(items, menuItem{Key: r.ID, Label: r.Name,
			Detail: itoa(len(r.APIEndpoints)) + " endpoints"})
	}
	s.menu.setItems(items)
	s.menu.setSize(s.app.width, maxInt(1, s.app.height-8))
}

func (s *wizardScreen) refreshActionLabels() {
	s.form.setActionLabel("recipe", "Recipe: "+s.recipeName())
}

func (s *wizardScreen) recipeName() string {
	if s.recipe == nil || s.recipe.Name == "" {
		return "(none)"
	}
	return s.recipe.Name
}

func (s *wizardScreen) handleKey(msg tea.KeyMsg) (Screen, tea.Cmd) {
	if s.picking {
		if msg.String() == "esc" {
			s.picking = false
			return s, nil
		}
		if key, ok := s.menu.update(msg); ok {
			s.selectRecipe(key)
			s.picking = false
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
		return s, pop()
	case "ctrl+n":
		return s, s.next()
	}
	action, handled, cmd := s.form.Update(msg)
	if handled && action != "" {
		switch action {
		case "recipe":
			s.picking = true
		case "next":
			return s, s.next()
		}
	}
	return s, cmd
}

func (s *wizardScreen) next() tea.Cmd {
	if strings.TrimSpace(s.name.Value()) == "" {
		s.err = "name is required"
		return nil
	}
	s.err = ""
	if s.recipe == nil {
		s.recipe = &models.Recipe{EnvVars: map[string]string{}}
	}
	return push(newRecipeEditorScreen(s.app, *s.recipe, s.recipe))
}

func (s *wizardScreen) selectRecipe(key string) {
	if key == "__none__" {
		s.recipe = &models.Recipe{EnvVars: map[string]string{}}
		return
	}
	for i := range s.recipes {
		if s.recipes[i].ID == key {
			r := s.recipes[i]
			s.recipe = &r
			return
		}
	}
	s.recipe = &models.Recipe{EnvVars: map[string]string{}}
}

// defaultBaseImageID picks a standard preset when the recipe has no base image.
func (s *wizardScreen) defaultBaseImageID() string {
	for _, img := range s.images {
		if img.IsInternal {
			return img.ID
		}
	}
	if len(s.images) > 0 {
		return s.images[0].ID
	}
	return ""
}

func (s *wizardScreen) create() tea.Cmd {
	name := strings.TrimSpace(s.name.Value())
	if name == "" {
		s.err = "name is required"
		return nil
	}
	recipe := *s.recipe
	if recipe.BaseImageID == "" {
		recipe.BaseImageID = s.defaultBaseImageID()
	}
	req := apitypes.InstanceCreateRequest{Name: name, Recipe: &recipe}
	return tea.Batch(setBusy(true, "Creating instance"), func() tea.Msg {
		inst, err := s.app.ctx.Client.CreateInstance(context.Background(), req)
		return wizardCreatedMsg{inst: inst, err: err}
	})
}

func (s *wizardScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render("New Instance") + "\n\n")
	if s.err != "" {
		b.WriteString(t.Error.Render(s.err) + "\n\n")
	}
	if s.picking {
		b.WriteString("Select recipe:\n\n" + s.menu.view() + "\n\n" + t.Dim.Render("Enter select · Esc cancel"))
		return b.String()
	}
	s.form.setTop(2)
	b.WriteString(s.form.view())
	b.WriteString("\n\n" + s.app.helpBar("Enter edit/activate", "↑↓ move", "Esc back"))
	return b.String()
}

// expandPath expands a leading ~ or relative path to a canonical absolute path.
func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = home + p[1:]
		}
	}
	if !filepath.IsAbs(p) {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}
	return filepath.Clean(p)
}
