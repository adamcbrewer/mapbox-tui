package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type importActivity struct {
	path             string
	completed, total int
	bar              *progress.Model
	failed           bool
}

type activityConfirmation struct {
	text    string
	expires time.Time
}

func (m *viewer) activityEnding() string {
	if m.settings.ASCII {
		return "..."
	}
	return "…"
}

func (m *viewer) busyMarker() string {
	if m.settings.NoAnimation {
		return "*"
	}
	return m.ui.spinner.View()
}

func (m *viewer) busyLabel(label string) string {
	return m.busyMarker() + " " + label + m.activityEnding()
}

func (m *viewer) importLabel(width int) string {
	a := &m.imports
	prefix, suffix := "Opening ", ""
	if a.total > 1 {
		prefix = fmt.Sprintf("Opening %d of %d · ", a.completed+1, a.total)
		suffix = fmt.Sprintf(" · %d completed", a.completed)
		if width < 60 {
			prefix = fmt.Sprintf("Opening %d/%d ", a.completed+1, a.total)
			suffix = fmt.Sprintf(" (%d done)", a.completed)
		}
		if width >= 80 {
			if a.bar == nil {
				bar := progress.New(progress.WithoutPercentage())
				a.bar = &bar
			}
			a.bar.SetWidth(8)
			a.bar.FullColor = lipgloss.Color(m.theme().Accent)
			a.bar.EmptyColor = lipgloss.Color(m.theme().ProgressEmpty)
			a.bar.Full, a.bar.Empty = '█', '░'
			if m.settings.ASCII {
				a.bar.Full, a.bar.Empty = '#', '-'
			}
			suffix += " " + a.bar.ViewAs(float64(a.completed)/float64(a.total))
		}
	}
	ending := m.activityEnding()
	room := max(1, width-ansi.StringWidth(prefix+suffix+ending))
	name := ansi.Truncate(cleanPlaceText(filepath.Base(a.path)), room, "")
	return prefix + name + ending + suffix
}

func (m *viewer) confirmActivity(text string) {
	m.confirmation = activityConfirmation{text: text, expires: time.Now().Add(2 * time.Second)}
	m.dirty = true
}

func (m *viewer) expireActivity(now time.Time) {
	if m.confirmation.text != "" && !now.Before(m.confirmation.expires) {
		m.confirmation = activityConfirmation{}
		m.dirty = true
	}
}

func (m *viewer) userActivities() []string {
	var labels []string
	if m.loading {
		labels = append(labels, m.importLabel(200))
	}
	if m.filePickerOpen() && m.ui.pickerBusy {
		labels = append(labels, "Reading folder"+m.activityEnding())
	}
	if m.planner.searchBusy {
		label := "Looking up location"
		if m.planner.savingFavourite {
			label = "Saving favourite"
		}
		labels = append(labels, label+m.activityEnding())
	}
	if m.ui.dialog == dialogSavedWorkspace && m.savedWork.busy {
		labels = append(labels, m.savedWorkspaceActivity()+m.activityEnding())
	}
	for _, item := range m.workspace.items() {
		if j := item.Item.Journey; j != nil && j.Busy {
			labels = append(labels, "Calculating "+strings.ToLower(j.Mode.label())+" route"+m.activityEnding())
		}
	}
	return labels
}

func (m *viewer) savedWorkspaceActivity() string {
	switch m.savedWork.mode {
	case "locate":
		return "Reading replacement file"
	case "library":
		if m.savedWork.opening {
			return "Opening workspace"
		}
		return "Reading saved workspaces"
	default:
		return "Opening workspace"
	}
}

func sourceActivityName(name string) string {
	if name == "traffic" {
		return "Traffic"
	}
	return "Basemap"
}

func (s *sourceState) activityFailure(compact bool) string {
	label := sourceActivityName(s.name)
	if compact {
		label += " error: "
		if s.retained {
			return label + "old data"
		}
		if len(s.snapshot.Features) > 0 {
			return label + "partial"
		}
		return label + "no data"
	}
	if s.retained {
		return label + " refresh failed · previous data shown"
	}
	if len(s.snapshot.Features) > 0 {
		return label + " load failed · partial data shown"
	}
	return label + " load failed · no data available"
}

func (m *viewer) backgroundActivities() []string {
	var labels []string
	for _, state := range m.sources.states {
		if !state.enabled || state.cancel == nil {
			continue
		}
		verb := "Loading "
		if len(state.snapshot.Features) > 0 || !state.snapshot.FetchedAt.IsZero() {
			verb = "Updating "
		}
		labels = append(labels, verb+state.name+m.activityEnding())
	}
	return labels
}

func (m *viewer) activityBusy() bool {
	return m.loading || m.sources.busy() || len(m.userActivities()) > 0
}

func (m *viewer) activityRow() string {
	backgroundColor := lipgloss.Color(m.theme().Background)
	accent := m.styles.accent.Background(backgroundColor)
	users, background := m.userActivities(), m.backgroundActivities()
	active := len(users) + len(background)
	for _, state := range m.sources.states {
		if !state.enabled || !state.snapshot.Failed {
			continue
		}
		suffix := " · alt+v details"
		if m.width >= 80 && active > 0 {
			suffix = fmt.Sprintf(" · %d active · alt+v details", active)
		}
		if m.width < 40 {
			suffix = " alt+v"
		}
		text := ansi.Truncate(state.activityFailure(m.width < 70), m.width-ansi.StringWidth(suffix), m.activityEnding())
		return accent.Bold(true).Render(pad(text+suffix, m.width))
	}
	labels := append(users, background...)
	if m.imports.failed && m.ui.dialog == dialogFiles {
		suffix := " · esc close"
		if m.imports.total > 1 {
			suffix = fmt.Sprintf(" · %d/%d opened", m.imports.completed, m.imports.total)
		}
		prefix := "Could not open "
		if m.width < 60 {
			prefix = "Failed: "
		}
		room := max(1, m.width-ansi.StringWidth(prefix+suffix))
		name := ansi.Truncate(cleanPlaceText(filepath.Base(m.imports.path)), room, m.activityEnding())
		return accent.Bold(true).Render(pad(prefix+name+suffix, m.width))
	}
	if len(users) == 0 && m.confirmation.text != "" {
		marker := "✓ "
		if m.settings.ASCII {
			marker = "OK "
		}
		return accent.Render(pad(marker+m.confirmation.text, m.width))
	}
	if len(labels) > 0 {
		suffix := ""
		if active > 1 {
			suffix = fmt.Sprintf(" · +%d active", active-1)
		}
		room := max(1, m.width-2-ansi.StringWidth(suffix))
		text := ansi.Truncate(labels[0], room, m.activityEnding())
		if m.loading {
			text = m.importLabel(room)
			if ansi.StringWidth(text) > room {
				suffix = ""
				text = m.importLabel(m.width - 2)
			}
		}
		return accent.Render(pad(m.busyMarker()+" "+text+suffix, m.width))
	}
	for _, source := range m.sources.view().Sources {
		if source.Status != "" {
			return m.styles.muted.Background(backgroundColor).Render(pad(cleanPlaceText(source.Status), m.width))
		}
	}
	return m.styles.text.Background(backgroundColor).Render(strings.Repeat(" ", m.width))
}

func (m *viewer) activityDetails() []string {
	lines := append(m.userActivities(), m.backgroundActivities()...)
	for _, state := range m.sources.states {
		if state.enabled && state.snapshot.Failed {
			line := state.activityFailure(false)
			if state.cancel != nil {
				line += "; retrying"
			} else if !state.snapshot.RefreshAt.IsZero() {
				line += "; retry scheduled"
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func (m *viewer) configureSpinner() {
	shape := spinner.MiniDot
	if m.settings.ASCII {
		shape = spinner.Line
	}
	if m.ui.spinner.Spinner.FPS != shape.FPS {
		m.ui.spinner = spinner.New(spinner.WithSpinner(shape), spinner.WithStyle(m.styles.accent))
		m.ui.spinning = false
	}
}
