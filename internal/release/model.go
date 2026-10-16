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
		reviewingNotes := m.session.Status == Confirmation && m.session.Step == Notes && m.input.Value() == ""
		if msg.String() == "r" && reviewingNotes && m.session.NotesPublic {
			m.session.reloadNotes()
			m.follow = true
			m.refresh()
			cmd := m.command()
			return m, cmd
		}
		if msg.String() == "e" && reviewingNotes && !m.session.NotesPublic {
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
	if m.height < 10 {
		return ""
	}
	if m.height < 20 {
		return ansi.Truncate(fmt.Sprintf("%s · %s · %d/9 complete", strings.ToUpper(string(m.session.Mode)), m.session.Version, m.session.Step), width, "…")
	}
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

func (m Model) promptForm() string {
	width := max(1, m.width-8)
	if m.editing {
		return ansi.Wrap("Release notes · ctrl+s save · esc back", width, "") + "\n" + m.editor.View()
	}
	var lines []string
	switch m.session.Status {
	case Ready:
		candidates, _ := m.session.Policy.Candidates()
		if m.height >= 20 {
			lines = append(lines, "Latest release: "+m.session.Policy.Latest,
				"Next: "+strings.Join(candidates, " / "))
		}
		if m.height >= 26 {
			lines = append(lines, "")
		}
		lines = append(lines, "Edit suggested tag, then enter to start.")
		if m.versionError != "" {
			lines = append(lines, m.style("#FF6B6B").Render(m.versionError))
		}
		lines = append(lines, m.input.View())
	case Confirmation:
		if m.session.Step == Notes {
			if strings.TrimSpace(m.session.Notes) == "" {
				lines = append(lines, m.style("#FF6B6B").Render(m.session.emptyNotesMessage()))
			}
			help := "↑/↓ or pgup/pgdown: scroll all notes · e: edit"
			if m.session.NotesPublic {
				help = "↑/↓ or pgup/pgdown: scroll all notes · r: refresh from GitHub"
			}
			lines = append(lines, help)
		}
		prompt := m.session.Prompt()
		if m.height < 12 && m.session.Step == Tag {
			action := "push tag"
			prompt = fmt.Sprintf("Would you like to %s %s now?\n%s @ %s", action, m.session.Version, m.session.Repository, shortCommit(m.session.Commit))
		}
		if m.height < 12 && m.session.Step == Publish && !m.session.NotesPublic {
			prompt = fmt.Sprintf("Have you published %s on GitHub?\n%s", m.session.Version, m.session.URL)
		}
		question, context, hasContext := strings.Cut(prompt, "\n")
		prompt = m.style("#F5D76E").Render(question + " [y/N]")
		if hasContext {
			prompt += "\n" + context
		}
		lines = append(lines, prompt,
			m.style("#D4D4D4").Render("Enter defaults to no · Esc cancels"), m.input.View())
	case Failed:
		lines = append(lines, "Enter: retry this operation")
	}
	return ansi.Wrap(strings.Join(lines, "\n"), width, "")
}

func (m Model) controls() string {
	width := max(1, m.width-4)
	if m.height < 20 && m.session.Status != Done {
		return ""
	}
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
func (m Model) checklist() string {
	var sections []string
	branchStyle := m.style("#707070").PaddingRight(1)
	for i, name := range stepNames {
		if Step(i) > m.session.Step {
			break
		}
		if m.height < 20 && Step(i) < m.session.Step {
			continue
		}
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
		}
		heading := m.style(color).Bold(Step(i) == m.session.Step).Render(fmt.Sprintf("%s %d  %s", marker, i+1, name))
		if Step(i) < m.session.Step {
			sections = append(sections, ansi.Wrap(heading, max(1, m.width-4), ""))
			continue
		}
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
		if Step(i) == Notes && m.session.Status == Confirmation && !m.editing {
			branch.Child(ansi.Wrap(safeText(m.session.Notes), max(1, m.width-8), ""))
		}
		sections = append(sections, branch.String())
	}
	return strings.Join(sections, "\n")
}

// Keep field groups distinct when space permits; compact terminals prioritise
// the input and enough room to inspect the scrolling checklist.
func (m Model) groupSeparator() string {
	if m.height >= 26 {
		return "\n\n"
	}
	return "\n"
}

// Short terminals spend their rows on the active step instead of fixed chrome.
func (m Model) chrome() string {
	var parts []string
	if header := m.header(); header != "" {
		parts = append(parts, header)
	}
	if controls := m.controls(); controls != "" {
		parts = append(parts, controls)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, m.groupSeparator()) + m.groupSeparator()
}

func (m Model) verticalPadding() int {
	if m.height < 20 {
		return 0
	}
	return 1
}

func (m Model) footer() string {
	if form := m.promptForm(); form != "" {
		return m.groupSeparator() + form
	}
	return ""
}

func (m *Model) refresh() {
	m.viewport.SetWidth(max(1, m.width-4))
	content := m.checklist()
	m.scrollable = false
	available := func() int {
		return max(1, m.height-2*m.verticalPadding()-strings.Count(m.chrome(), "\n")-strings.Count(m.footer(), "\n"))
	}
	m.scrollable = lipgloss.Height(content) > available()
	m.viewport.SetHeight(available())
	m.viewport.SetContent(content)
	if m.follow {
		m.viewport.GotoBottom()
	}
}

func (m Model) View() tea.View {
	content := m.chrome() + m.viewport.View() + m.footer()
	if m.noColor {
		content = ansi.Strip(content)
	}
	view := tea.NewView(lipgloss.NewStyle().Padding(m.verticalPadding(), 2).Render(content))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m Model) Summary() string { return m.session.Summary() }
