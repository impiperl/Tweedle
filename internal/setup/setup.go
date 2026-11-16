// Package setup is the interactive selection of watched processes and
// containers ("tweedel setup").
package setup

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/impiperl/Tweedle/internal/collect"
	"github.com/impiperl/Tweedle/internal/config"
	"github.com/impiperl/Tweedle/internal/i18n"
	"github.com/impiperl/Tweedle/internal/monitor"
)

const maxCmdline = 200

type kind int

const (
	kindContainer kind = iota
	kindProcess
)

type item struct {
	kind     kind
	title    string
	detail   string
	user     string
	count    int
	running  bool
	selected bool
	proc     config.Process // for processes: the entry that will be saved
	existing bool           // proc comes from the current config
}

// Run shows the selection for cfg and, if confirmed, returns the updated
// configuration (ok=false on cancel).
func Run(cfg *config.Config) (*config.Config, bool, error) {
	items, podmanErr, err := buildItems(cfg)
	if err != nil {
		return nil, false, err
	}
	m := &model{lang: cfg.Lang, items: items, podmanErr: podmanErr, height: 24, width: 100}
	m.applyFilter()
	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return nil, false, err
	}
	fm := final.(*model)
	if !fm.saved {
		return nil, false, nil
	}
	out := *cfg
	out.Containers, out.Processes = nil, nil
	usedNames := map[string]bool{}
	for _, it := range fm.items {
		if !it.selected {
			continue
		}
		switch it.kind {
		case kindContainer:
			out.Containers = append(out.Containers, config.Container{Name: it.title})
		case kindProcess:
			p := it.proc
			if usedNames[p.Name] {
				if it.existing {
					continue // one config entry can cover several listed groups
				}
				p.Name = uniqueName(p.Name, usedNames)
			}
			usedNames[p.Name] = true
			out.Processes = append(out.Processes, p)
		}
	}
	return &out, true, nil
}

func uniqueName(base string, used map[string]bool) string {
	for i := 2; ; i++ {
		n := base + "-" + strconv.Itoa(i)
		if !used[n] {
			return n
		}
	}
}

func buildItems(cfg *config.Config) ([]item, string, error) {
	var items []item
	podmanErr := ""

	// Containers
	containers, err := collect.ListContainers(cfg.PodmanCmd)
	if err != nil {
		podmanErr = err.Error()
	}
	wanted := map[string]bool{}
	for _, c := range cfg.Containers {
		wanted[c.Name] = true
	}
	sort.Slice(containers, func(i, j int) bool { return containers[i].Name < containers[j].Name })
	for _, c := range containers {
		items = append(items, item{kind: kindContainer, title: c.Name, detail: c.Image + " · " + c.Status,
			running: c.State == "running", selected: wanted[c.Name]})
		delete(wanted, c.Name)
	}
	for _, c := range cfg.Containers {
		if wanted[c.Name] {
			items = append(items, item{kind: kindContainer, title: c.Name, selected: true})
		}
	}

	// Processes, grouped by name and (shortened) command line
	procs, err := collect.NewProcessCollector().List(true)
	if err != nil {
		return nil, "", err
	}
	type group struct {
		name, cmd string
		count     int
		users     map[string]bool
	}
	groups := map[string]*group{}
	groupsPerName := map[string]int{}
	for _, p := range procs {
		cmd := cut(p.Cmdline, maxCmdline) // plain cut: must stay a substring for matching
		k := p.Name + "\x00" + cmd
		g, ok := groups[k]
		if !ok {
			g = &group{name: p.Name, cmd: cmd, users: map[string]bool{}}
			groups[k] = g
			groupsPerName[p.Name]++
		}
		g.count++
		if p.User != "" {
			g.users[p.User] = true
		}
	}
	matched := make([]bool, len(cfg.Processes))
	var procItems []item
	for _, g := range groups {
		it := item{kind: kindProcess, title: g.name, detail: g.cmd, count: g.count, running: true,
			user: strings.Join(sortedKeys(g.users), ",")}
		for i, want := range cfg.Processes {
			if monitor.Matches(want, g.name, g.cmd) {
				it.selected, it.existing, it.proc = true, true, want
				matched[i] = true
				break
			}
		}
		if !it.existing {
			it.proc = config.Process{Name: g.name, Exe: g.name, MinCount: 1}
			// The name alone is enough if only one kind of this process runs.
			if groupsPerName[g.name] > 1 {
				it.proc.Cmdline = g.cmd
			}
		}
		procItems = append(procItems, it)
	}
	for i, want := range cfg.Processes {
		if !matched[i] {
			procItems = append(procItems, item{kind: kindProcess, title: want.Exe, detail: want.Cmdline,
				selected: true, existing: true, proc: want})
		}
	}
	sort.Slice(procItems, func(i, j int) bool {
		if procItems[i].title != procItems[j].title {
			return strings.ToLower(procItems[i].title) < strings.ToLower(procItems[j].title)
		}
		return procItems[i].detail < procItems[j].detail
	})
	return append(items, procItems...), podmanErr, nil
}

// --- bubbletea model ------------------------------------------------------

type model struct {
	lang      string
	items     []item
	visible   []int // indices into items matching the filter
	cursor    int   // index into visible
	offset    int
	filter    string
	filtering bool
	podmanErr string
	width     int
	height    int
	saved     bool
}

var (
	styleTitle   = lipgloss.NewStyle().Bold(true)
	styleDim     = lipgloss.NewStyle().Faint(true)
	styleCursor  = lipgloss.NewStyle().Reverse(true)
	styleHeading = lipgloss.NewStyle().Bold(true).Underline(true)
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if m.filtering {
			switch msg.Type {
			case tea.KeyEnter, tea.KeyEsc:
				m.filtering = false
			case tea.KeyBackspace:
				if r := []rune(m.filter); len(r) > 0 {
					m.filter = string(r[:len(r)-1])
					m.applyFilter()
				}
			case tea.KeyRunes, tea.KeySpace:
				m.filter += string(msg.Runes)
				m.applyFilter()
			case tea.KeyCtrlC:
				return m, tea.Quit
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "enter":
			m.saved = true
			return m, tea.Quit
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "pgup":
			m.move(-m.listHeight())
		case "pgdown":
			m.move(m.listHeight())
		case "home", "g":
			m.move(-len(m.visible))
		case "end", "G":
			m.move(len(m.visible))
		case " ", "x":
			if len(m.visible) > 0 {
				it := &m.items[m.visible[m.cursor]]
				it.selected = !it.selected
			}
		case "/":
			m.filtering = true
		case "esc":
			m.filter = ""
			m.applyFilter()
		}
	}
	return m, nil
}

func (m *model) move(d int) {
	m.cursor += d
	if m.cursor >= len(m.visible) {
		m.cursor = len(m.visible) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *model) applyFilter() {
	f := strings.ToLower(m.filter)
	m.visible = m.visible[:0]
	for i, it := range m.items {
		if f == "" || strings.Contains(strings.ToLower(it.title+" "+it.detail+" "+it.user), f) {
			m.visible = append(m.visible, i)
		}
	}
	m.move(0)
}

// listHeight is the number of item lines that fit below the header.
func (m *model) listHeight() int {
	h := m.height - 7 // header, help, filter, error, two headings, footer
	if h < 3 {
		h = 3
	}
	return h
}

func (m *model) View() string {
	var b strings.Builder
	t := func(code string, p map[string]string) string { return i18n.T(m.lang, code, p) }
	b.WriteString(styleTitle.Render(t("setup_title", nil)) + "\n")
	b.WriteString(styleDim.Render(t("setup_help", nil)) + "\n")
	if m.filtering || m.filter != "" {
		cur := ""
		if m.filtering {
			cur = "█"
		}
		b.WriteString(t("setup_filter", nil) + m.filter + cur + "\n")
	} else {
		b.WriteString("\n")
	}
	if m.podmanErr != "" {
		b.WriteString(styleErr.Render(truncate(t("setup_podman_err", map[string]string{"error": m.podmanErr}), m.width)) + "\n")
	}

	lh := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+lh {
		m.offset = m.cursor - lh + 1
	}
	end := m.offset + lh
	if end > len(m.visible) {
		end = len(m.visible)
	}
	lastKind := kind(-1)
	for vi := m.offset; vi < end; vi++ {
		it := m.items[m.visible[vi]]
		if it.kind != lastKind {
			h := "setup_containers"
			if it.kind == kindProcess {
				h = "setup_processes"
			}
			b.WriteString(styleHeading.Render(t(h, nil)) + "\n")
			lastKind = it.kind
		}
		b.WriteString(m.renderItem(it, vi == m.cursor) + "\n")
	}

	n := 0
	for _, it := range m.items {
		if it.selected {
			n++
		}
	}
	b.WriteString(styleDim.Render(fmt.Sprintf("%s · %d/%d", t("setup_selected", map[string]string{"n": strconv.Itoa(n)}),
		min(m.cursor+1, len(m.visible)), len(m.visible))))
	return b.String()
}

func (m *model) renderItem(it item, cursor bool) string {
	box := "[ ]"
	if it.selected {
		box = "[x]"
	}
	title := it.title
	if it.kind == kindProcess && it.count > 1 {
		title += " ×" + strconv.Itoa(it.count)
	}
	line := fmt.Sprintf("%s %-22s", box, truncate(title, 22))
	if it.user != "" {
		line += " " + fmt.Sprintf("%-8s", truncate(it.user, 8))
	}
	detail := it.detail
	if !it.running {
		detail = i18n.T(m.lang, "setup_not_running", nil) + " " + detail
	}
	line = truncate(line+" "+detail, max(m.width-1, 20))
	if cursor {
		return styleCursor.Render(line)
	}
	if !it.running {
		return styleDim.Render(line)
	}
	return line
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
