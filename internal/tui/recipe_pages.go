package tui

import (
	"context"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

// listSavedMsg reports the outcome of an asynchronous list save.
type listSavedMsg struct{ err error }

// inlineKeyCreatedMsg reports a newly created inline API key.
type inlineKeyCreatedMsg struct {
	key models.APIKey
	err error
}

// ---- Base image picker ----

type baseImagePickerScreen struct {
	app    *App
	menu   *menu
	choose func(id string)
}

func newBaseImagePickerScreen(app *App, images []apitypes.BaseImageView, choose func(string)) *baseImagePickerScreen {
	s := &baseImagePickerScreen{app: app, menu: newMenu(app.theme), choose: choose}
	items := make([]menuItem, 0, len(images))
	for _, img := range images {
		items = append(items, menuItem{Key: img.ID, Label: img.Name, Detail: img.SourcePathOrRepo})
	}
	if len(items) == 0 {
		items = append(items, menuItem{Header: true, Label: "NO BASE IMAGES"})
	}
	s.menu.setItems(items)
	s.menu.setSize(app.width, maxInt(1, app.height-6))
	return s
}

func (s *baseImagePickerScreen) ID() string    { return "base-image-picker" }
func (s *baseImagePickerScreen) Title() string { return "Base Image" }
func (s *baseImagePickerScreen) Init() tea.Cmd { return nil }

func (s *baseImagePickerScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
		return s, pop()
	}
	if id, ok := s.menu.update(msg); ok {
		if s.choose != nil {
			s.choose(id)
		}
		return s, pop()
	}
	return s, nil
}

func (s *baseImagePickerScreen) View() string {
	t := s.app.theme
	s.menu.setTop(2)
	return t.Title.Render("Select Base Image") + "\n\n" + s.menu.view() +
		"\n\n" + s.app.helpBar("Enter select", "Esc back")
}

// ---- Environment variables editor ----

type envVarsEditorScreen struct {
	app       *App
	area      textarea.Model
	form      *form
	focusArea bool
	err       string
	save      func(map[string]string) tea.Cmd
}

func newEnvVarsEditorScreen(app *App, env map[string]string, save func(map[string]string) tea.Cmd) *envVarsEditorScreen {
	s := &envVarsEditorScreen{app: app, save: save, focusArea: true}
	s.area = textarea.New()
	s.area.Placeholder = "KEY=VALUE, one line per entry"
	s.area.SetValue(envToText(env))
	s.area.SetHeight(8)
	s.area.Focus()
	s.form = newForm(app.theme)
	s.form.addButton(button{Key: "save", Label: "Save", Hotkey: "C-s"}, button{Key: "cancel", Label: "Cancel", Hotkey: "Esc"})
	return s
}

func (s *envVarsEditorScreen) ID() string    { return "env-editor" }
func (s *envVarsEditorScreen) Title() string { return "Environment Variables" }

func (s *envVarsEditorScreen) Init() tea.Cmd { return nil }

func (s *envVarsEditorScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if s.focusArea {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "esc":
				return s, pop()
			case "tab":
				s.focusArea = false
				s.area.Blur()
				return s, nil
			case "ctrl+s":
				return s, s.commit()
			}
		}
		var cmd tea.Cmd
		s.area, cmd = s.area.Update(msg)
		return s, cmd
	}
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
		return s, pop()
	}
	action, _, cmd := s.form.Update(msg)
	switch action {
	case "save":
		return s, s.commit()
	case "cancel":
		return s, pop()
	}
	return s, cmd
}

func (s *envVarsEditorScreen) commit() tea.Cmd {
	env := textToEnv(s.area.Value())
	if s.save != nil {
		return tea.Batch(s.save(env), pop())
	}
	return pop()
}

func (s *envVarsEditorScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render("Environment Variables") + "\n\n")
	b.WriteString(s.area.View() + "\n\n")
	s.form.setTop(11)
	b.WriteString(s.form.view())
	b.WriteString("\n\n" + t.Dim.Render("Tab to buttons · Enter activate · Esc back"))
	return b.String()
}

// ---- Mounts editor ----

type mountsEditorScreen struct {
	app       *App
	title     string
	mounts    []models.Mount
	save      func([]models.Mount) tea.Cmd
	popOnSave bool
	menu      *menu
	adding    bool
	form      *form
	host      *field
	guest     *field
	rw        bool
	err       string
}

func newMountsEditorScreen(app *App, title string, mounts []models.Mount, save func([]models.Mount) tea.Cmd) *mountsEditorScreen {
	s := &mountsEditorScreen{
		app: app, title: title, save: save, rw: true,
		mounts: append([]models.Mount{}, mounts...),
	}
	s.menu = newMenu(app.theme)
	s.form = newForm(app.theme)
	s.host = newField(app.theme, "Host Path", "/host/path", false)
	s.guest = newField(app.theme, "Guest Path", "/mnt/target", false)
	s.form.addField(s.host)
	s.form.addField(s.guest)
	s.form.addAction("mode", "Mode: rw")
	s.form.addButton(button{Key: "add", Label: "Add Mount", Hotkey: "C-a"}, button{Key: "cancel", Label: "Cancel", Hotkey: "Esc"})
	s.rebuild()
	return s
}

func (s *mountsEditorScreen) ID() string    { return "mounts-editor" }
func (s *mountsEditorScreen) Title() string { return s.title }
func (s *mountsEditorScreen) Init() tea.Cmd { return nil }

func (s *mountsEditorScreen) rebuild() {
	items := make([]menuItem, 0, len(s.mounts)+1)
	for i, m := range s.mounts {
		items = append(items, menuItem{Key: "mount:" + itoa(i), Label: m.HostPath,
			Detail: m.GuestPath + " (" + string(m.Mode) + ")"})
	}
	if len(items) == 0 {
		items = append(items, menuItem{Header: true, Label: "NO MOUNTS"})
	}
	items = append(items, menuItem{Key: "add", Label: "＋ Add Mount"})
	s.menu.setItems(items)
	s.menu.setSize(s.app.width, maxInt(1, s.app.height-8))
}

func (s *mountsEditorScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if saved, ok := msg.(listSavedMsg); ok {
		if saved.err != nil {
			s.err = saved.err.Error()
			return s, setBusy(false, "")
		}
		if s.popOnSave {
			return s, pop()
		}
		return s, setBusy(false, "")
	}
	if s.adding {
		if s.form.Capturing() {
			_, _, cmd := s.form.Update(msg)
			return s, cmd
		}
		if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
			s.adding = false
			return s, nil
		}
		action, _, cmd := s.form.Update(msg)
		switch action {
		case "add":
			return s, s.add()
		case "cancel":
			s.adding = false
			return s, nil
		case "mode":
			s.rw = !s.rw
			s.form.setActionLabel("mode", "Mode: "+rwLabel(s.rw))
		}
		return s, cmd
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			return s, pop()
		case "a":
			return s, s.beginAdd()
		case "d":
			return s, s.removeSelected()
		}
	}
	if id, ok := s.menu.update(msg); ok && id == "add" {
		return s, s.beginAdd()
	}
	return s, nil
}

func (s *mountsEditorScreen) beginAdd() tea.Cmd {
	s.adding = true
	s.err = ""
	s.rw = true
	s.form.setActionLabel("mode", "Mode: rw")
	s.host.SetValue("")
	s.guest.SetValue("")
	return nil
}

func (s *mountsEditorScreen) removeSelected() tea.Cmd {
	id, ok := s.menu.selected()
	if !ok || !strings.HasPrefix(id, "mount:") {
		return nil
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(id, "mount:"))
	if err != nil || idx < 0 || idx >= len(s.mounts) {
		return nil
	}
	s.mounts = append(s.mounts[:idx], s.mounts[idx+1:]...)
	s.rebuild()
	return s.persist()
}

func (s *mountsEditorScreen) add() tea.Cmd {
	host := expandPath(s.host.Value())
	guest := strings.TrimSpace(s.guest.Value())
	if host == "" {
		s.err = "host path is required"
		return nil
	}
	if guest == "" {
		guest = "/mnt/" + lastSegment(host)
	}
	mode := models.MountReadOnly
	if s.rw {
		mode = models.MountReadWrite
	}
	s.mounts = append(s.mounts, models.Mount{HostPath: host, GuestPath: guest, Mode: mode})
	s.err = ""
	s.adding = false
	s.rebuild()
	return s.persist()
}

func (s *mountsEditorScreen) persist() tea.Cmd {
	if s.save == nil {
		return nil
	}
	return s.save(s.mounts)
}

func (s *mountsEditorScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render(s.title) + "\n\n")
	if s.err != "" {
		b.WriteString(t.Error.Render(s.err) + "\n\n")
	}
	if s.adding {
		s.form.setTop(2)
		b.WriteString(s.form.view() + "\n\n" + t.Dim.Render("Enter edit/activate · ↑↓ move · Esc cancel"))
		return b.String()
	}
	s.menu.setTop(2)
	b.WriteString(s.menu.view())
	b.WriteString("\n\n" + s.app.helpBar("a add", "d delete", "Esc back"))
	return b.String()
}

func rwLabel(rw bool) string {
	if rw {
		return "rw"
	}
	return "ro"
}

// ---- Resources editor ----

type resourcesEditorScreen struct {
	app       *App
	title     string
	resources []models.Resource
	save      func([]models.Resource) tea.Cmd
	menu      *menu
	adding    bool
	form      *form
	host      *field
	guest     *field
	exec      bool
	err       string
}

func newResourcesEditorScreen(app *App, title string, resources []models.Resource, save func([]models.Resource) tea.Cmd) *resourcesEditorScreen {
	s := &resourcesEditorScreen{app: app, title: title, save: save,
		resources: append([]models.Resource{}, resources...)}
	s.menu = newMenu(app.theme)
	s.form = newForm(app.theme)
	s.host = newField(app.theme, "Host Source", "/host/file", false)
	s.guest = newField(app.theme, "Guest Target", "/guest/file", false)
	s.form.addField(s.host)
	s.form.addField(s.guest)
	s.form.addAction("mode", "File Mode: 0644")
	s.form.addButton(button{Key: "add", Label: "Add Resource", Hotkey: "C-a"}, button{Key: "cancel", Label: "Cancel", Hotkey: "Esc"})
	s.rebuild()
	return s
}

func (s *resourcesEditorScreen) ID() string    { return "resources-editor" }
func (s *resourcesEditorScreen) Title() string { return s.title }
func (s *resourcesEditorScreen) Init() tea.Cmd { return nil }

func (s *resourcesEditorScreen) rebuild() {
	items := make([]menuItem, 0, len(s.resources)+1)
	for i, r := range s.resources {
		items = append(items, menuItem{Key: "res:" + itoa(i), Label: r.HostSourcePath,
			Detail: r.GuestTargetPath + " (" + r.FileMode + ")"})
	}
	if len(items) == 0 {
		items = append(items, menuItem{Header: true, Label: "NO RESOURCES"})
	}
	items = append(items, menuItem{Key: "add", Label: "＋ Add Resource"})
	s.menu.setItems(items)
	s.menu.setSize(s.app.width, maxInt(1, s.app.height-8))
}

func (s *resourcesEditorScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if s.adding {
		if s.form.Capturing() {
			_, _, cmd := s.form.Update(msg)
			return s, cmd
		}
		if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
			s.adding = false
			return s, nil
		}
		action, _, cmd := s.form.Update(msg)
		switch action {
		case "add":
			return s, s.add()
		case "cancel":
			s.adding = false
			return s, nil
		case "mode":
			s.exec = !s.exec
			s.form.setActionLabel("mode", "File Mode: "+resMode(s.exec))
		}
		return s, cmd
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			return s, pop()
		case "a":
			return s, s.beginAdd()
		case "d":
			return s, s.removeSelected()
		}
	}
	if id, ok := s.menu.update(msg); ok && id == "add" {
		return s, s.beginAdd()
	}
	return s, nil
}

func (s *resourcesEditorScreen) beginAdd() tea.Cmd {
	s.adding = true
	s.err = ""
	s.exec = false
	s.form.setActionLabel("mode", "File Mode: 0644")
	s.host.SetValue("")
	s.guest.SetValue("")
	return nil
}

func (s *resourcesEditorScreen) removeSelected() tea.Cmd {
	id, ok := s.menu.selected()
	if !ok || !strings.HasPrefix(id, "res:") {
		return nil
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(id, "res:"))
	if err != nil || idx < 0 || idx >= len(s.resources) {
		return nil
	}
	s.resources = append(s.resources[:idx], s.resources[idx+1:]...)
	s.rebuild()
	return s.persist()
}

func (s *resourcesEditorScreen) add() tea.Cmd {
	host := expandPath(s.host.Value())
	guest := strings.TrimSpace(s.guest.Value())
	if host == "" || guest == "" {
		s.err = "host and guest paths are required"
		return nil
	}
	s.resources = append(s.resources, models.Resource{
		HostSourcePath: host, GuestTargetPath: guest, FileMode: resMode(s.exec),
	})
	s.err = ""
	s.adding = false
	s.rebuild()
	return s.persist()
}

func (s *resourcesEditorScreen) persist() tea.Cmd {
	if s.save == nil {
		return nil
	}
	return s.save(s.resources)
}

func (s *resourcesEditorScreen) View() string {
	t := s.app.theme
	var b strings.Builder
	b.WriteString(t.Title.Render(s.title) + "\n\n")
	if s.err != "" {
		b.WriteString(t.Error.Render(s.err) + "\n\n")
	}
	if s.adding {
		s.form.setTop(2)
		b.WriteString(s.form.view() + "\n\n" + t.Dim.Render("Enter edit/activate · ↑↓ move · Esc cancel"))
		return b.String()
	}
	s.menu.setTop(2)
	b.WriteString(s.menu.view())
	b.WriteString("\n\n" + s.app.helpBar("a add", "d delete", "Esc back"))
	return b.String()
}

func resMode(exec bool) string {
	if exec {
		return "0755"
	}
	return "0644"
}

// ---- GPUs editor ----

type gpusEditorScreen struct {
	app       *App
	title     string
	gpus      []models.GPU
	selected  map[string]bool
	save      func([]models.GPU) tea.Cmd
	popOnSave bool
	menu      *menu
}

func newGPUsEditorScreen(app *App, title string, gpus []models.GPU, chosen []models.GPU, save func([]models.GPU) tea.Cmd) *gpusEditorScreen {
	s := &gpusEditorScreen{app: app, title: title, gpus: gpus, save: save, selected: map[string]bool{}}
	for _, g := range chosen {
		s.selected[g.DeviceID] = true
	}
	s.menu = newMenu(app.theme)
	s.rebuild()
	return s
}

func (s *gpusEditorScreen) ID() string    { return "gpus-editor" }
func (s *gpusEditorScreen) Title() string { return s.title }
func (s *gpusEditorScreen) Init() tea.Cmd { return nil }

func (s *gpusEditorScreen) rebuild() {
	items := make([]menuItem, 0, len(s.gpus))
	for _, g := range s.gpus {
		mark := "[ ]"
		if s.selected[g.DeviceID] {
			mark = "[x]"
		}
		items = append(items, menuItem{Key: g.DeviceID, Label: mark + " " + g.Name,
			Detail: g.DeviceID + " " + g.RenderNode})
	}
	if len(items) == 0 {
		items = append(items, menuItem{Header: true, Label: "NO AMD GPUS DETECTED"})
	}
	s.menu.setItems(items)
	s.menu.setSize(s.app.width, maxInt(1, s.app.height-6))
}

func (s *gpusEditorScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if saved, ok := msg.(listSavedMsg); ok {
		if saved.err != nil {
			return s, setStatus("GPU update failed: "+saved.err.Error(), true)
		}
		if s.popOnSave {
			return s, pop()
		}
		return s, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			return s, pop()
		case " ", "enter":
			if id, ok := s.menu.selected(); ok {
				s.selected[id] = !s.selected[id]
				s.rebuild()
				return s, s.persist()
			}
		}
	}
	s.menu.update(msg)
	return s, nil
}

func (s *gpusEditorScreen) persist() tea.Cmd {
	if s.save == nil {
		return nil
	}
	var out []models.GPU
	for _, g := range s.gpus {
		if s.selected[g.DeviceID] {
			out = append(out, g)
		}
	}
	return s.save(out)
}

func (s *gpusEditorScreen) View() string {
	t := s.app.theme
	s.menu.setTop(2)
	return t.Title.Render(s.title) + "\n\n" + s.menu.view() +
		"\n\n" + s.app.helpBar("Space toggle", "Esc back")
}

// ---- API endpoints editor ----

type endpointsEditorScreen struct {
	app      *App
	parent   *recipeEditorScreen
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

func newEndpointsEditorScreen(app *App, parent *recipeEditorScreen) *endpointsEditorScreen {
	s := &endpointsEditorScreen{
		app: app, parent: parent, menu: newMenu(app.theme), provider: models.ProviderCustom,
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
func (s *endpointsEditorScreen) Init() tea.Cmd { return nil }

func (s *endpointsEditorScreen) rebuild() {
	items := make([]menuItem, 0, len(s.parent.recipe.APIEndpoints)+2)
	for i, e := range s.parent.recipe.APIEndpoints {
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

func (s *endpointsEditorScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if created, ok := msg.(inlineKeyCreatedMsg); ok {
		if created.err != nil {
			s.err = created.err.Error()
			return s, nil
		}
		s.parent.addEndpoint(created.key)
		s.parent.keys = append(s.parent.keys, apitypes.APIKeyView{APIKey: created.key, HasSecret: true})
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
			if k, found := s.parent.findKey(id); found {
				s.parent.addEndpoint(k)
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
			return s, pop()
		case "d":
			return s, s.removeSelected()
		}
	}
	if id, ok := s.menu.update(msg); ok {
		switch id {
		case "pick":
			s.mode = "pick"
			s.menu.setItems(s.parent.keyItems())
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

func (s *endpointsEditorScreen) removeSelected() tea.Cmd {
	id, ok := s.menu.selected()
	if !ok || !strings.HasPrefix(id, "ep:") {
		return nil
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(id, "ep:"))
	if err != nil || idx < 0 || idx >= len(s.parent.recipe.APIEndpoints) {
		return nil
	}
	s.parent.recipe.APIEndpoints = append(s.parent.recipe.APIEndpoints[:idx], s.parent.recipe.APIEndpoints[idx+1:]...)
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
