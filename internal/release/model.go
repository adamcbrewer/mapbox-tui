package release

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
	"github.com/charmbracelet/x/ansi"
)

type tickMsg struct{}

type Model struct {
	session       Session
	interval      time.Duration
	started       time.Time
	elapsed       time.Duration
	spinner       spinner.Model
	input         textinput.Model
	viewport      viewport.Model
	width, height int
	noColor       bool
	scrollable    bool
	follow        bool
	versionError  string
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
		viewport: viewport.New(), width: 80, height: 30, noColor: noColor, follow: true,
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
		m.input.SetWidth(max(1, min(30, m.width-6)))
		m.refresh()
		return m, nil
	case tickMsg:
		if m.session.Status != Running {
			return m, nil
		}
		m.elapsed = time.Since(m.started).Truncate(time.Second)
		previous := m.session.Step
		m.session.Advance()
		if m.session.Step != previous {
			m.follow = true
		}
		m.refresh()
		if m.session.Status == Running {
			return m, m.tick()
		}
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.session.Cancel()
			return m, tea.Quit

		}
		scroll := msg.String() == "up" || msg.String() == "down" || msg.String() == "pgup" || msg.String() == "pgdown"
		if scroll {
			m.follow = false
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		if msg.String() == "enter" {
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
				return m, m.tick()
			}
			return m, nil
		}
		if m.session.Status == Ready || m.session.Status == Confirmation {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			m.refresh()
			return m, cmd
		}
	case tea.MouseWheelMsg:
		m.follow = false
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
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
	identity := fmt.Sprintf("%s · example/mapbox-tui · 0123456789ab", m.session.Version)
	if m.width < 60 {
		identity = m.session.Version + " · demo repository"
	}
	return ansi.Wrap(fmt.Sprintf("%s\n%s\n%s\n%s  %d/9 steps complete",
		title, m.style("#A0A0A0").Render("DEMO · simulated release"), m.style("#A0A0A0").Render(identity),
		bar.ViewAs(float64(m.session.Step)/float64(Complete)), m.session.Step,
	), width, "")
}

func (m Model) controls() string {
	width := max(1, m.width-4)
	framed := m.height >= 26
	formWidth := width
	if framed {
		formWidth -= 2
	}
	var lines []string
	switch m.session.Status {
	case Ready:
		candidates, _ := m.session.Policy.Candidates()
		lines = append(lines, "Latest release: "+m.session.Policy.Latest+" (demo)",
			"Next: "+strings.Join(candidates, " / "))
		if m.height >= 26 {
			lines = append(lines, "")
		}
		lines = append(lines, "Edit suggested tag, then enter to start.")
		if m.versionError != "" {
			lines = append(lines, m.style("#FFFFFF").Render(m.versionError))
		}
		lines = append(lines, m.input.View())
	case Confirmation:
		lines = append(lines, m.session.Prompt(), m.style("#D4D4D4").Render("Type yes, then enter."), m.input.View())
	case Failed:
		lines = append(lines, m.style("#FFFFFF").Render(m.session.Detail), "Enter: simulate repair and retry")
	case Done:
		lines = append(lines, "Demo complete. No real release was created. Enter: exit")
	case Running:
		lines = append(lines, m.spinner.View()+" "+m.session.Step.String())
	}
	form := ansi.Wrap(strings.Join(lines, "\n"), formWidth, "")
	if framed {
		form = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color("#707070")).PaddingLeft(1).Render(form)
	}
	hint := func(key, description string) string {
		return m.style("#D4D4D4").Render(key) + " " + m.style("#909090").Render(description)
	}
	help := m.style("#909090").Render(fmt.Sprintf("Elapsed %s", m.elapsed)) + " · " + hint("esc", "cancel")
	if m.scrollable {
		help += "\n" + hint("↑/↓ · pgup/pgdown · wheel", "scroll")
	}

	if form == "" {
		return ansi.Wrap(help, width, "")
	}
	return form + m.groupSeparator() + ansi.Wrap(help, width, "")
}

// checklist uses Lip Gloss's tree renderer for wrapped child activity under each
// numbered step. The viewport follows the active step until the user scrolls.
func (m Model) checklist() (string, int) {
	var sections []string
	activeLine := 0
	for i, name := range stepNames {
		marker, color := "○", "#A0A0A0"
		if Step(i) < m.session.Step {
			marker, color = "✓", "#73D69C"
		}
		if Step(i) == m.session.Step {
			marker, color = "▶", "#FFFFFF"
			if m.session.Status == Failed {
				marker, color = "!", "#FFFFFF"
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
			EnumeratorStyle(m.style("#707070"))
		for _, entry := range m.session.Activity[i] {
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
				prefix = "! "
			case ActivityCancelled:
				prefix = "– "
			}
			branch.Child(m.style("#C0C0C0").Render(ansi.Wrap(prefix+entry.Text, max(1, m.width-8), "")))
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
	m.scrollable = false
	chrome := lipgloss.Height(m.header()) + lipgloss.Height(m.controls()) + 2*len(m.groupSeparator()) + 2
	m.scrollable = lipgloss.Height(content) > max(1, m.height-chrome)
	chrome = lipgloss.Height(m.header()) + lipgloss.Height(m.controls()) + 2*len(m.groupSeparator()) + 2
	m.viewport.SetHeight(max(1, m.height-chrome))
	m.viewport.SetContent(content)
	if m.follow {
		m.viewport.SetYOffset(max(0, active-1))
		if m.session.Status == Done {
			m.viewport.GotoBottom()
		}
	}
}

func (m Model) View() tea.View {
	content := m.header() + m.groupSeparator() + m.controls() + m.groupSeparator() + m.viewport.View()
	if m.height < 20 || m.width < 36 {
		content = ansi.Wrap("Release demo: enlarge terminal to 36 columns and 20 rows. Esc cancels.", max(1, m.width-4), "")
	}
	if m.noColor {
		content = ansi.Strip(content)
	}
	view := tea.NewView(lipgloss.NewStyle().Padding(1, 2).Render(content))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m Model) Summary() string {
	if m.session.Status == Done {
		return "Demo complete. No real release was created."
	}
	return "Demo cancelled. No commands ran; no remote workflow is running."
}
