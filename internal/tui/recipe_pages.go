package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"vivarium/internal/apitypes"
	"vivarium/internal/models"
)

// listSavedMsg reports the outcome of an asynchronous list save.
type listSavedMsg struct{ err error }

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
		"\n\n" + t.Dim.Render("GPUs are bound at instance creation and cannot be changed later.") +
		"\n" + s.app.helpBar("Space toggle", "Esc back")
}
