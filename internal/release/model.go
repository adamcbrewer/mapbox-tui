package release

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
	"github.com/charmbracelet/x/ansi"
)

type tickMsg struct{}

type Model struct {
	session        Session
	interval       time.Duration
	started        time.Time
	elapsed        time.Duration
	spinner        spinner.Model
	input          textinput.Model
	viewport       viewport.Model
	width, height  int
	noColor        bool
	scrollable     bool
	follow         bool
	versionError   string
	adapter        Adapter
	ctx            context.Context
	cancel         context.CancelFunc
	busy, stopping bool
	execution      *executionLifecycle
	output         chan string
	editor         textarea.Model
	editing        bool
}

func NewModel(session Session, interval time.Duration, noColor bool) Model {
	input := textinput.New()
	input.Prompt = "> "
	input.CharLimit = 128
	input.SetWidth(24)
	if session.Status == Ready {
		input.SetValue(session.Version)
	}
	input.Focus()
	m := Model{
		session: session, interval: interval, spinner: spinner.New(spinner.WithSpinner(spinner.Dot)), input: input,
		viewport: viewport.New(), editor: textarea.New(), width: 80, height: 30, noColor: noColor, follow: true,
	}
	m.spinner.Style = m.style("#FFFFFF")
	m.refresh()
	return m
}

func (m Model) Init() tea.Cmd { return m.spinner.Tick }

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.height < 20 || m.width < 36 {
		switch input := msg.(type) {
		case tea.KeyPressMsg:
			if input.String() != "esc" && input.String() != "ctrl+c" {
				return m, nil
			}
			// Escape cancels even when the notes editor is hidden.
			m.editing = false
		case tea.PasteMsg:
			return m, nil
		}
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
		m.input.SetWidth(max(1, min(30, m.width-10)))
		m.editor.SetWidth(max(1, m.width-8))
		m.editor.SetHeight(max(3, min(10, m.height-17)))
		m.refresh()
		return m, nil
	case operationResultMsg:
		m.busy = false
		m.drainOutput()
		if m.stopping {
			return m, tea.Quit
		}
		previous := m.session.Step
		m.session.Accept(msg.result, msg.err)
		if m.session.Step != previous {
			m.follow = true
		}
		m.refresh()
		if m.session.Status == Running {
			if msg.result.Pending {
				return m, tea.Tick(m.retryDelay(), func(time.Time) tea.Msg { return tickMsg{} })
			}
			cmd := m.command()
			return m, cmd
		}
		return m, nil
	case tickMsg:
		if m.session.Status != Running {
			return m, nil
		}
		m.elapsed = time.Since(m.started).Truncate(time.Second)
		if m.adapter != nil {
			cmd := m.command()
			return m, cmd
		}
		previous := m.session.Step
		m.session.Advance()
		if m.session.Step != previous {
			m.follow = true
		}
		m.refresh()
		if m.session.Status == Running {
			cmd := m.command()
			return m, cmd
		}
		return m, nil
	case tea.KeyPressMsg:
		if m.editing {
			switch msg.String() {
			case "ctrl+s":
				m.session.Notes = m.editor.Value()
				m.editing = false
				m.refresh()
				return m, nil
			case "esc":
				m.editing = false
				m.refresh()
				return m, nil
			case "ctrl+c":
			default:
				var cmd tea.Cmd
				m.editor, cmd = m.editor.Update(msg)
				m.follow = true
				m.refresh()
				return m, cmd
			}
		}
		if msg.String() == "e" && m.session.Status == Confirmation && m.session.Step == Notes && m.input.Value() == "" {
			m.editor = textarea.New()
			m.editor.CharLimit = 128 * 1024
			m.editor.SetValue(m.session.Notes)
			m.editor.SetWidth(max(1, m.width-8))
			m.editor.SetHeight(max(3, min(10, m.height-17)))
			m.editing = true
			cmd := m.editor.Focus()
			m.follow = true
			m.refresh()
			return m, cmd
		}
		switch msg.String() {
		case "ctrl+c", "esc":
			m.session.Cancel()
			if m.cancel != nil {
				m.cancel()
			}
			if m.busy {
				m.stopping = true
				m.refresh()
				return m, nil
			}
			return m, tea.Quit
		case "up", "down", "pgup", "pgdown":
			m.follow = false
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case "enter":
			if !m.follow && (m.session.Status == Ready || m.session.Status == Confirmation || m.session.Status == Failed) {
				m.follow = true
				m.refresh()
				return m, nil
			}
			switch m.session.Status {
			case Ready:
				if err := m.session.SelectVersion(m.input.Value()); err != nil {
					m.versionError = err.Error()
					m.refresh()
					return m, nil
				}
				m.versionError = ""
				m.input.SetValue("")
				m.started = time.Now()
				m.session.Start()
			case Confirmation:
				m.session.Confirm(m.input.Value())
				m.input.SetValue("")
			case Failed:
				m.session.Retry()
			case Done:
				return m, tea.Quit
			default:
				return m, nil
			}
			m.follow = true
			m.refresh()
			if m.session.Status == Running {
				cmd := m.command()
				return m, cmd
			}
			return m, nil
		}

	case tea.MouseWheelMsg:
		m.follow = false
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case spinner.TickMsg:
		m.drainOutput()
		if !m.started.IsZero() {
			m.elapsed = time.Since(m.started).Truncate(time.Second)
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.refresh()
		return m, cmd
	}
	if m.editing {
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		m.refresh()
		return m, cmd
	}
	if m.session.Status == Ready || m.session.Status == Confirmation {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		switch msg.(type) {
		case tea.KeyPressMsg, tea.PasteMsg:
			m.follow = true
		}
		m.refresh()
		return m, cmd
	}
	return m, nil
}

func (m Model) style(color string) lipgloss.Style {
	if m.noColor {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}

func (m Model) header() string {
	width := max(1, m.width-4)
	bar := progress.New(
		progress.WithWidth(max(1, min(28, width-21))),
		progress.WithColors(lipgloss.Color("#4DE8E0"), lipgloss.Color("#FF4FB7")),
		progress.WithoutPercentage(),
		progress.WithScaled(true),
	)
	bar.EmptyColor = lipgloss.Color("#484B54")
	title := m.style("#FFFFFF").Bold(true).Render("MAPBOX / RELEASE")
	identity := fmt.Sprintf("%s · %s · %s", m.session.Version, m.session.Repository, shortCommit(m.session.Commit))
	if m.width < 60 {
		identity = m.session.Version + " · " + m.session.Repository
	}
	modeLabel := map[Mode]string{TestMode: "TEST · simulated release", Rehearsal: "REHEARSAL · no remote writes", Live: "RELEASE · real git and GitHub operations"}[m.session.Mode]
	return ansi.Wrap(fmt.Sprintf("%s\n%s\n%s\n%s  %d/9 steps complete",
		title, m.style("#A0A0A0").Render(modeLabel), m.style("#A0A0A0").Render(identity),
		bar.ViewAs(float64(m.session.Step)/float64(Complete)), m.session.Step,
	), width, "")
}

func (m Model) inlineForm() string {
	width := max(1, m.width-8)
	if m.editing {
		return ansi.Wrap("Release notes · ctrl+s save · esc back", width, "") + "\n" + m.editor.View()
	}
	var lines []string
	switch m.session.Status {
	case Ready:
		candidates, _ := m.session.Policy.Candidates()
		lines = append(lines, "Latest release: "+m.session.Policy.Latest,
			"Next: "+strings.Join(candidates, " / "))
		if m.height >= 26 {
			lines = append(lines, "")
		}
		lines = append(lines, "Edit suggested tag, then enter to start.")
		if m.versionError != "" {
			lines = append(lines, m.style("#FF6B6B").Render(m.versionError))
		}
		lines = append(lines, m.input.View())
	case Confirmation:
		lines = append(lines, m.session.Prompt(), m.style("#D4D4D4").Render("Type yes, then enter."), m.input.View())
	case Failed:
		lines = append(lines, "Enter: retry this operation")
	}
	return ansi.Wrap(strings.Join(lines, "\n"), width, "")
}

func (m Model) controls() string {
	width := max(1, m.width-4)
	hint := func(key, description string) string {
		return m.style("#D4D4D4").Render(key) + " " + m.style("#909090").Render(description)
	}
	help := m.style("#909090").Render(fmt.Sprintf("Elapsed %s", m.elapsed)) + " · " + hint("esc", "cancel")
	if m.scrollable {
		help += "\n" + hint("↑/↓ · pgup/pgdown · wheel", "scroll")
	}

	if m.session.Status == Done {
		return ansi.Wrap(m.session.Summary()+" Enter: exit", width, "")
	}
	return ansi.Wrap(help, width, "")
}

// checklist uses Lip Gloss's tree renderer for wrapped child activity under each
// numbered step. The viewport follows the active step until the user scrolls.
func (m Model) checklist() (string, int) {
	var sections []string
	activeLine := 0
	branchStyle := m.style("#707070").PaddingRight(1)
	for i, name := range stepNames {
		marker, color := "○", "#A0A0A0"
		if m.session.Skipped[i] {
			marker, color = "–", "#A0A0A0"
		} else if Step(i) < m.session.Step {
			marker, color = "✓", "#73D69C"
		}
		if Step(i) == m.session.Step {
			marker, color = "▶", "#FFFFFF"
			if m.session.Status == Failed {
				marker, color = "✗", "#FF6B6B"
			}
			if m.session.Status == Confirmation {
				marker, color = "?", "#FFFFFF"
			}
			activeLine = lipgloss.Height(strings.Join(sections, "\n"))
			if len(sections) == 0 {
				activeLine = 0
			}
		}
		heading := m.style(color).Bold(Step(i) == m.session.Step).Render(fmt.Sprintf("%s %d  %s", marker, i+1, name))
		branch := tree.Root(ansi.Wrap(heading, max(1, m.width-4), "")).
			Enumerator(tree.RoundedEnumerator).
			EnumeratorStyle(branchStyle).
			IndenterStyle(branchStyle)
		for _, entry := range m.session.Activity[i] {
			if Step(i) == m.session.Step && m.session.Status == Confirmation && entry.Text == m.session.Prompt() {
				continue
			}
			prefix := "· "
			switch entry.Status {
			case ActivityRunning:
				activeLine = lipgloss.Height(strings.Join(sections, "\n")) + lipgloss.Height(branch.String())
				if len(sections) == 0 {
					activeLine--
				}
				prefix = m.spinner.View() + " "
			case ActivityComplete:
				prefix = "✓ "
			case ActivityFailed:
				prefix = m.style("#FF6B6B").Render("✗") + " "
			case ActivityCancelled:
				prefix = "– "
			}
			leaf := tree.Root(m.style("#C0C0C0").Render(ansi.Wrap(prefix+entry.Text, max(1, m.width-9), ""))).
				EnumeratorStyle(branchStyle).
				IndenterStyle(branchStyle)
			for _, line := range entry.Output {
				leaf.Child(m.style("#A0A0A0").Render(ansi.Wrap(line, max(1, m.width-14), "")))
			}
			branch.Child(leaf)
		}
		if Step(i) == m.session.Step {
			if form := m.inlineForm(); form != "" {
				branch.Child(form)
				activeLine = lipgloss.Height(strings.Join(sections, "\n")) + lipgloss.Height(branch.String()) - 1
				if len(sections) == 0 {
					activeLine--
				}
			}
		}
		sections = append(sections, branch.String())
	}
	return strings.Join(sections, "\n"), activeLine
}

// Keep field groups distinct when space permits; compact terminals prioritise
// the input and enough room to inspect the scrolling checklist.
func (m Model) groupSeparator() string {
	if m.height >= 26 {
		return "\n\n"
	}
	return "\n"
}

func (m *Model) refresh() {
	m.viewport.SetWidth(max(1, m.width-4))
	content, active := m.checklist()
	chrome := lipgloss.Height(m.header()) + 2*len(m.groupSeparator()) + 2
	// Check overflow without the scroll hint, then reserve room for it if needed.
	m.scrollable = false
	m.scrollable = lipgloss.Height(content) > max(1, m.height-chrome-lipgloss.Height(m.controls()))
	chrome += lipgloss.Height(m.controls())
	m.viewport.SetHeight(max(1, m.height-chrome))
	m.viewport.SetContent(content)
	if m.follow {
		offset := active - 1
		if m.inlineForm() != "" {
			offset = active - m.viewport.Height() + 1
		}
		m.viewport.SetYOffset(max(0, offset))
		if m.session.Status == Done {
			m.viewport.GotoBottom()
		}
	}
}

func (m Model) View() tea.View {
	content := m.header() + m.groupSeparator() + m.controls() + m.groupSeparator() + m.viewport.View()
	if m.height < 20 || m.width < 36 {
		content = ansi.Wrap("Release: enlarge terminal to 36 columns and 20 rows. Esc cancels.", max(1, m.width-4), "")
	}
	if m.noColor {
		content = ansi.Strip(content)
	}
	view := tea.NewView(lipgloss.NewStyle().Padding(1, 2).Render(content))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m Model) Summary() string { return m.session.Summary() }
