package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type loaded struct {
	path     string
	id       uint64
	track    *Track
	playback *playback
	err      error
}
type sceneMsg struct {
	track    *Track
	features []Feature
	stats    Stats
	status   string
	camera   Camera
}
type pulse time.Time
type viewer struct {
	path                      string
	args                      Args
	track                     *Track
	provider                  *Provider
	ctx                       context.Context
	camera                    Camera
	width, height             int
	selected                  int
	features                  []Feature
	stats                     Stats
	dirty, busy, loading      bool
	status, content           string
	changed                   time.Time
	fetched                   Camera
	dragging, rotating, moved bool
	lastX, lastY              int
	inspected                 *XY
	fatal                     error
	wheel                     wheelZoom
	playback                  *playback
	lastTick, lastFetch       time.Time
	showProfile               bool
	profileHeight             int
	profile                   *elevationProfile
	progress                  *progress.Model
	ui                        uiState
	cursor                    *tea.Cursor
	loadID                    uint64
	following                 bool
	motion                    cameraMotion
	lastMotionTick            time.Time
}

func tick() tea.Cmd {
	return tea.Tick(16*time.Millisecond, func(t time.Time) tea.Msg { return pulse(t) })
}
func (m *viewer) Init() tea.Cmd {
	if m.path == "" {
		return tea.Batch(tick(), m.freeRoam())
	}
	return tea.Batch(tick(), m.load(m.path))
}
func (m *viewer) freeRoam() tea.Cmd {
	m.loadID++
	m.path, m.track, m.playback = "", nil, nil
	m.args.PointIndex, m.args.Lon, m.args.Lat, m.args.Zoom, m.args.Bearing = nil, nil, nil, nil, nil
	m.camera = m.destination()
	m.motion.Active = false
	m.loading, m.following = false, false
	m.selected = -1
	m.profile, m.inspected = nil, nil
	m.features = nil
	m.ui.details.GotoTop()
	m.lastMotionTick = time.Now()
	m.layout()
	m.dirty = true
	if m.busy {
		return nil
	}
	return m.fetch()
}
func (m *viewer) load(path string) tea.Cmd {
	m.loadID++
	id := m.loadID
	m.loading = true
	m.dirty = true
	return tea.Batch(m.startSpinner(), func() tea.Msg {
		t, e := loadTrack(path)
		var p *playback
		if e == nil {
			p = newPlayback(t)
		}
		return loaded{path, id, t, p, e}
	})
}
func mapSize(w, h int) (int, int) {
	if w >= 90 {
		return max(1, w-32), max(1, h-6)
	}
	return max(1, w-2), max(1, h-9)
}
func (m *viewer) fetch() tea.Cmd {
	m.busy = true
	m.lastFetch = time.Now()
	c, track := m.destination(), m.track
	return tea.Batch(m.startSpinner(), func() tea.Msg { f, s, e := m.provider.scene(m.ctx, track, c); return sceneMsg{track, f, s, e, c} })
}
func (m *viewer) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.layout()
		if m.ui.dialog == dialogFiles {
			m.ui.picker.SetHeight(max(1, min(17, m.height-10)-2))
			m.ui.picker, _ = m.ui.picker.Update(msg)
		}
		m.dirty = true
		m.changed = time.Now()
	case loaded:
		if v.id != m.loadID {
			return m, nil
		}
		m.loading = false
		if v.err != nil {
			m.dirty = true
			cmd := m.openDialog(dialogFiles)
			m.ui.notice = "Could not open track file: " + v.err.Error()
			return m, cmd
		}
		if m.track != nil {
			m.args.PointIndex, m.args.Lon, m.args.Lat, m.args.Zoom, m.args.Bearing = nil, nil, nil, nil, nil
		}
		m.path = v.path
		m.track = v.track
		m.playback = v.playback
		m.features = nil
		m.motion.Active = false
		m.following = false
		m.profile = nil
		m.inspected = nil
		m.ui.details.GotoTop()
		w, h := mapSize(m.width, m.height)
		h -= profileRows(h, m.showProfile)
		a := m.args
		vw, vh := max(10, min(300, w)), max(5, min(100, h))
		a.Width = &vw
		a.Height = &vh
		c, e := viewport(m.track, a)
		if e != nil {
			m.fatal = e
			return m, tea.Quit
		}
		m.camera = c
		m.camera.Width, m.camera.Height = w, h
		m.selected = -1
		if len(m.track.Points) > 0 {
			m.selected = 0
		}
		if m.args.PointIndex != nil {
			m.selected = *m.args.PointIndex
			if m.selected < 0 || m.selected >= len(m.track.Points) {
				m.fatal = fmt.Errorf("point_index outside track")
				return m, tea.Quit
			}
		}
		m.playback.seek(m.selected)
		m.lastTick = time.Now()
		m.lastMotionTick = m.lastTick
		m.layout()
		m.dirty = true
		if m.busy {
			return m, nil
		}
		return m, m.fetch()
	case sceneMsg:
		m.busy = false
		if v.track != m.track {
			return m, m.fetch()
		}
		m.features, m.stats, m.status = v.features, v.stats, v.status
		m.fetched = v.camera
		m.dirty = true
	case pulse:
		now := time.Now()
		seconds := now.Sub(m.lastMotionTick).Seconds()
		m.lastMotionTick = now
		m.advancePlayback(now)
		if m.motion.advance(&m.camera, seconds) {
			m.dirty = true
		}
		if !m.busy && !m.loading {
			m.ui.spinning = false
		}
		if m.dirty {
			m.rebuild()
			m.dirty = false
		}
		ready := time.Since(m.changed) > 100*time.Millisecond || (m.playback != nil && m.playback.Playing && time.Since(m.lastFetch) > 250*time.Millisecond)
		if !m.busy && !m.loading && m.provider.Kind == "mapbox" && m.destination() != m.fetched && ready {
			return m, tea.Batch(tick(), m.fetch())
		}
		return m, tick()
	case spinner.TickMsg:
		if m.ui.spinning {
			var cmd tea.Cmd
			m.ui.spinner, cmd = m.ui.spinner.Update(msg)
			m.dirty = true
			return m, cmd
		}
	case tea.KeyPressMsg:
		key := v.String()
		if key == "ctrl+c" || (key == "q" && m.ui.dialog != dialogCommands && m.ui.dialog != dialogFiles) {
			return m, tea.Quit
		}
		if m.ui.dialog != dialogNone {
			return m, m.updateDialog(msg)
		}
		switch key {
		case "q":
			return m, tea.Quit
		case "ctrl+o", "o":
			return m, m.openDialog(dialogFiles)
		case "ctrl+p":
			return m, m.openDialog(dialogCommands)
		case "?":
			return m, m.openDialog(dialogHelp)
		case "v":
			return m, m.openDialog(dialogCredits)
		case "p":
			if m.playback != nil {
				return m, m.openDialog(dialogSpeed)
			}
			return m, nil
		case "enter":
			return m, m.openDialog(dialogInspect)
		case "tab":
			if m.width >= 90 {
				m.ui.detailsFocused = !m.ui.detailsFocused
				m.dirty = true
				return m, nil
			}
			return m, m.openDialog(dialogInspect)
		}
		if m.ui.detailsFocused {
			switch key {
			case "up", "down", "left", "right", "h", "j", "k", "l", "pgup", "pgdown", "home", "end":
				var cmd tea.Cmd
				m.ui.details, cmd = m.ui.details.Update(msg)
				m.dirty = true
				return m, cmd
			case "esc":
				m.ui.detailsFocused = false
				m.dirty = true
				return m, nil
			}
		}
		if m.track == nil {
			switch key {
			case "[", "]", "pgup", "pgdown", "home", "end", "c", "e", "space", "s", "S":
				return m, nil
			}
		}
		m.advancePlayback(time.Now())
		seeking := false
		c := &m.camera
		switch key {
		case "left", "h":
			m.panCamera(8, 0)
		case "right", "l":
			m.panCamera(-8, 0)
		case "up", "k":
			m.panCamera(0, 8)
		case "down", "j":
			m.panCamera(0, -8)
		case "+", "=":
			m.motion.Active = false
			c.zoomAt(XY{float64(c.Width), float64(c.Height * 2)}, zoomStep)
		case "-":
			m.motion.Active = false
			c.zoomAt(XY{float64(c.Width), float64(c.Height * 2)}, -zoomStep)
		case "f", "r":
			m.following = false
			m.moveCamera(fit(m.track, c.Width, c.Height, c.Aspect))
		case "n":
			m.motion.Active = false
			c.Bearing = 0
		case ",":
			m.motion.Active = false
			c.Bearing -= 10
		case ".":
			m.motion.Active = false
			c.Bearing += 10
		case "[":
			seeking = true
			m.selected--
			m.inspected = nil
		case "]":
			seeking = true
			m.selected++
			m.inspected = nil
		case "pgdown":
			seeking = true
			m.selected += 100
			m.inspected = nil
		case "pgup":
			seeking = true
			m.selected -= 100
			m.inspected = nil
		case "home":
			seeking = true
			m.selected = 0
			m.inspected = nil
		case "end":
			seeking = true
			m.selected = len(m.track.Points) - 1
			m.inspected = nil
		case "c":
			if pos := m.position(); pos != nil {
				target := m.destination()
				target.Center = pos.World
				m.following = true
				m.moveCamera(target)
			}
		case "e":
			m.showProfile = !m.showProfile
			m.layout()
		case "space":
			if m.playback != nil {
				m.playback.toggle()
				m.selected = m.playback.nearest()
				m.inspected = nil
			}
		case "s", "S":
			if m.playback != nil {
				direction := 1
				if key == "S" {
					direction = -1
				}
				m.playback.cycle(direction)
			}
		case "a":
			m.args.ASCII = !m.args.ASCII
		case "m":
			m.args.NoAnimation = !m.args.NoAnimation
			if m.args.NoAnimation {
				m.camera = m.destination()
				m.motion.Active = false
			}
		default:
			return m, nil
		}
		if m.track != nil && len(m.track.Points) > 0 {
			m.selected = max(0, min(len(m.track.Points)-1, m.selected))
		} else {
			m.selected = -1
		}
		if seeking && m.playback != nil {
			m.playback.seek(m.selected)
		}
		m.dirty = true
		m.changed = time.Now()
	case tea.MouseMsg:
		if m.ui.dialog != dialogNone {
			return m, m.updateDialog(msg)
		}
		mouse := v.Mouse()
		m.advancePlayback(time.Now())
		x, y := mouse.X, mouse.Y
		inside := x >= 1 && x < m.camera.Width+1 && y >= 2 && y < m.camera.Height+2
		if !inside && !m.dragging && !m.rotating && m.width >= 90 && x >= m.camera.Width+4 {
			var cmd tea.Cmd
			m.ui.details, cmd = m.ui.details.Update(msg)
			m.dirty = true
			return m, cmd
		}
		switch v.(type) {
		case tea.MouseClickMsg:
			if inside && (mouse.Button == tea.MouseLeft || mouse.Button == tea.MouseRight) {
				m.motion.Active = false
				m.following = false
				m.ui.detailsFocused = false
				m.dragging = mouse.Button == tea.MouseLeft
				m.rotating = mouse.Button == tea.MouseRight
				m.moved = false
				m.lastX, m.lastY = x, y
			}
		case tea.MouseMotionMsg:
			if m.dragging || m.rotating {
				dx, dy := x-m.lastX, y-m.lastY
				if dx != 0 || dy != 0 {
					m.moved = true
					if m.rotating {
						m.camera.Bearing += float64(dx) * 2
					} else {
						m.following = false
						m.camera.pan(float64(dx*2), float64(dy*4))
					}
					m.lastX, m.lastY = x, y
					m.dirty = true
					m.changed = time.Now()
				}
			}
		case tea.MouseReleaseMsg:
			if m.dragging && !m.moved && inside {
				m.pausePlayback()
				p := XY{float64((x-1)*2 + 1), float64((y-2)*4 + 2)}
				world := unproject(m.camera.world(p))
				m.inspected = &world
				if i := pick(m.track, m.camera, p); i >= 0 {
					m.selected = i
					if m.playback != nil {
						m.playback.seek(i)
					}
					m.inspected = nil
				}
				m.dirty = true
			}
			m.dragging, m.rotating = false, false
		case tea.MouseWheelMsg:
			if inside && (mouse.Button == tea.MouseWheelUp || mouse.Button == tea.MouseWheelDown) {
				direction := -1
				if mouse.Button == tea.MouseWheelUp {
					direction = 1
				}
				m.zoomWheel(XY{float64((x-1)*2 + 1), float64((y-2)*4 + 2)}, direction, mouse.Mod, time.Now())
			}
		}
	default:
		if m.ui.dialog != dialogNone {
			return m, m.updateDialog(msg)
		}
	}
	return m, nil
}

func (m *viewer) layout() {
	w, h := mapSize(m.width, m.height)
	m.profileHeight = profileRows(h, m.showProfile && m.track != nil)
	m.camera.Width, m.camera.Height = w, h-m.profileHeight
	m.motion.Target.Width, m.motion.Target.Height = m.camera.Width, m.camera.Height
	if m.width < 90 {
		m.ui.detailsFocused = false
	}
	if m.profileHeight > 0 && m.track != nil {
		width, height := max(1, m.width-2), m.profileHeight-2
		if m.profile == nil || m.profile.Grid.Width != width || m.profile.Grid.Height != height {
			m.profile = newProfile(m.track, width, height)
		}
	}
}
func (m *viewer) pausePlayback() {
	if m.playback != nil {
		m.playback.Playing = false
		m.dirty = true
	}
}
func (m *viewer) position() *ridePosition {
	if m.track == nil || m.selected < 0 || m.selected >= len(m.track.Points) {
		return nil
	}
	if m.playback != nil {
		return m.playback.position(m.track)
	}
	p := m.track.Points[m.selected]
	return &ridePosition{Index: m.selected, World: p.World, Distance: p.Distance, Elevation: p.Elevation, Time: p.Time}
}
func (m *viewer) advancePlayback(now time.Time) {
	if m.playback == nil || m.track == nil {
		return
	}
	if !m.lastTick.IsZero() && m.playback.advance(m.track, now.Sub(m.lastTick).Seconds()) {
		m.selected = m.playback.nearest()
		m.inspected = nil
		m.dirty = true
		if pos := m.position(); pos != nil && m.following {
			target := m.destination()
			if followPosition(&target, pos.World) {
				m.moveCamera(target)
			}
		}
	}
	m.lastTick = now
}

func gridRow(g Grid, y int, ascii bool) string {
	var line strings.Builder
	for x := 0; x < g.Width; {
		layer := g.Cells[y*g.Width+x].Layer
		var run strings.Builder
		for x < g.Width && g.Cells[y*g.Width+x].Layer == layer {
			run.WriteRune(g.glyph(g.Cells[y*g.Width+x], ascii))
			x++
		}
		line.WriteString(paint(run.String(), layer))
	}
	return line.String()
}

func (m *viewer) zoomWheel(p XY, direction int, mod tea.KeyMod, now time.Time) {
	delta := m.wheel.step(direction, now)
	if delta == 0 {
		return
	}
	if mod&(tea.ModCtrl|tea.ModSuper|tea.ModMeta) != 0 {
		delta *= 2
	}
	m.motion.Active = false
	m.camera.zoomAt(p, delta)
	m.dirty = true
	m.changed = now
}

var palette = []string{"#8D9DAA", "#548EAB", "#536575", "#303F4C", "#A7BCCB", "#FFC66D", "#FFC66D", "#F2F6FA"}

func paint(s string, layer int) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(palette[min(layer, 7)])).Render(s)
}
func pad(s string, w int) string {
	s = ansi.Truncate(s, max(0, w), "")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}
func (m *viewer) details() []string {
	t := m.track
	if t == nil {
		center := unproject(m.camera.Center)
		lines := []string{"Map details", "", "Free roam", "No route loaded", "ctrl+o to open a track file", "", "Map center", fmt.Sprintf("Lat %.6f", center.Y), fmt.Sprintf("Lon %.6f", center.X)}
		if m.inspected != nil {
			lines = append(lines, "", "Selected position", fmt.Sprintf("Lat %.6f", m.inspected.Y), fmt.Sprintf("Lon %.6f", m.inspected.X))
		}
		return append(lines, "", fmt.Sprintf("Zoom %.2f", m.camera.Zoom), fmt.Sprintf("Bearing %.0f°", math.Mod(m.camera.Bearing+360, 360)), "", "Tiles · req / hit / err", fmt.Sprintf("%d / %d / %d", m.stats.Requests, m.stats.Hits, m.stats.Errors))
	}
	lines := []string{"Route details", "", fmt.Sprintf("%.2f km", t.Distance/1000), fmt.Sprintf("%d points / %d parts", len(t.Points), len(t.Segments)), fmt.Sprintf("%d waypoints", len(t.Waypoints)), "", "Selected position"}
	if m.inspected != nil {
		lines = append(lines, fmt.Sprintf("Lat %.6f", m.inspected.Y), fmt.Sprintf("Lon %.6f", m.inspected.X), "Off-track location")
	} else if pos := m.position(); pos != nil {
		p := t.Points[pos.Index]
		ele, stamp := "unavailable", "unavailable"
		if pos.Elevation != nil {
			ele = fmt.Sprintf("%.1f m", *pos.Elevation)
		}
		if pos.Time != nil {
			stamp = *pos.Time
		}
		label := fmt.Sprintf("Point %d / %d", pos.Index+1, len(t.Points))
		lon, lat := p.Lon, p.Lat
		if pos.Blend > 0 {
			label = fmt.Sprintf("Between %d–%d / %d", pos.Index+1, pos.Index+2, len(t.Points))
			geo := unproject(pos.World)
			lon, lat = geo.X, geo.Y
			if pos.Elevation != nil {
				ele = "~" + ele
			}
			if pos.Time != nil {
				stamp = "~" + stamp
			}
		}
		lines = append(lines, label, fmt.Sprintf("Part %d (%s)", p.Segment+1, t.Segments[p.Segment].Kind), fmt.Sprintf("%.2f km along", pos.Distance/1000), fmt.Sprintf("Lat %.6f", lat), fmt.Sprintf("Lon %.6f", lon), "Elevation: "+ele, stamp)
	} else {
		lines = append(lines, "No track points")
	}
	mode := "Free camera · c to follow"
	if m.following {
		mode = "Following position"
	}
	lines = append(lines, "", fmt.Sprintf("Zoom %.2f", m.camera.Zoom), fmt.Sprintf("Bearing %.0f°", math.Mod(m.camera.Bearing+360, 360)), mode, "", "S start · E end", "W waypoint · @ position", "", "Tiles · req / hit / err", fmt.Sprintf("%d / %d / %d", m.stats.Requests, m.stats.Hits, m.stats.Errors))
	return lines
}
func (m *viewer) rebuild() {
	m.cursor = nil
	if m.width < 30 || m.height < 12 {
		m.content = "Enlarge terminal to 30×12; q quits"
		return
	}
	c := m.camera
	g := render(m.track, c, m.features, -1, m.args.ASCII)
	pos := m.position()
	if pos != nil {
		g.label(c.screen(pos.World), "@", 7, false)
	}
	details := m.details()
	provider := uiAccent.Background(uiSelected).Padding(0, 1).Render(m.provider.Kind)
	name := "Free roam"
	if m.track != nil {
		name = m.track.Name
	}
	title := gradient("shoe", uiInk) + "  " + uiHeading.Render(name)
	rows := []string{pad(title, m.width-lipgloss.Width(provider)-1) + " " + provider}
	border := lipgloss.NormalBorder()
	if m.args.ASCII {
		border = lipgloss.ASCIIBorder()
	}
	mapRows := []string{}
	for y := 0; y < c.Height; y++ {
		mapRows = append(mapRows, gridRow(g, y, m.args.ASCII))
	}
	frame := lipgloss.NewStyle().Border(border).BorderForeground(lipgloss.Color("#43556B")).Render(strings.Join(mapRows, "\n"))
	if m.width >= 90 {
		styled := []string{}
		for i, line := range details {
			style := uiText
			if i == 0 || line == "Selected position" {
				style = uiHeading.Foreground(uiViolet)
			} else if i > 16 || strings.Contains(line, "unavailable") {
				style = uiMuted
			}
			styled = append(styled, style.Render(line))
		}
		m.ui.details.SetWidth(28)
		m.ui.details.SetHeight(c.Height)
		m.ui.details.SetContent(strings.Join(styled, "\n"))
		focus := uiMuted.Render("tab focus · wheel scroll")
		if m.ui.detailsFocused {
			focus = uiAccent.Render("Details focused · tab map")
		}
		panel := "\n" + m.ui.details.View() + "\n" + focus
		frame = lipgloss.JoinHorizontal(lipgloss.Top, frame, "  "+strings.ReplaceAll(panel, "\n", "\n  "))
	}
	rows = append(rows, frame)
	if m.width < 90 {
		if m.track != nil {
			rows = append(rows, paint(pad(fmt.Sprintf("%.2f km | %d points | zoom %.2f | bearing %.0f", m.track.Distance/1000, len(m.track.Points), c.Zoom, c.Bearing), m.width), 5))
			s := strings.Join(details[7:min(len(details), 14)], " | ")
			rows = append(rows, uiText.Render(pad(s, m.width)), uiMuted.Render(pad("[ ] select; PgUp/PgDn ±100; c follow · enter inspect", m.width)))
		} else {
			center := unproject(c.Center)
			location := fmt.Sprintf("Lat %.6f | Lon %.6f", center.Y, center.X)
			if m.inspected != nil {
				location = fmt.Sprintf("Selected: %.6f, %.6f", m.inspected.Y, m.inspected.X)
			}
			rows = append(rows, paint(pad(fmt.Sprintf("Free roam | zoom %.2f | bearing %.0f", c.Zoom, c.Bearing), m.width), 5), uiText.Render(pad(location, m.width)), uiMuted.Render(pad("ctrl+o open file · enter inspect · f reset world view", m.width)))
		}
	}
	if m.profileHeight > 0 && m.profile != nil {
		header, chart, axis := m.profile.frame(pos)
		rows = append(rows, uiHeading.Foreground(uiViolet).Render(pad(header, m.width)))
		for y := 0; y < chart.Height; y++ {
			rows = append(rows, " "+gridRow(chart, y, m.args.ASCII)+" ")
		}
		rows = append(rows, paint(pad(" "+axis, m.width), 2))
	}
	status := "Left-drag pan · wheel zoom · click inspect · right-drag rotate"
	if m.playback != nil {
		status = m.playback.status()
	}
	if m.status != "" {
		status = m.status + " | " + status
	}
	if m.busy || m.loading {
		label := "Loading basemap…"
		if m.loading {
			label = "Loading track file…"
		}
		status = m.ui.spinner.View() + " " + label + " " + status
	}
	if m.playback != nil {
		if m.progress == nil {
			bar := progress.New(progress.WithColors(lipgloss.Color("#FFC66D")), progress.WithoutPercentage(), progress.WithFillCharacters('█', '░'))
			bar.EmptyColor = lipgloss.Color("#536575")
			m.progress = &bar
		}
		m.progress.SetWidth(max(6, min(14, m.width/6)))
		m.progress.Full, m.progress.Empty = '█', '░'
		if m.args.ASCII {
			m.progress.Full, m.progress.Empty = '#', '-'
		}
		layer := 4
		if m.playback.Playing {
			layer = 5
		}
		status = m.progress.ViewAs(m.playback.ratio()) + "  " + paint(status, layer)
	}
	m.ui.help.SetWidth(m.width)
	keys := shortcutMap{compact: m.width < 90, playing: m.playback != nil && m.playback.Playing, detailsFocused: m.ui.detailsFocused, freeRoam: m.track == nil}
	rows = append(rows, pad(status, m.width), pad(m.ui.help.View(keys), m.width), uiMuted.Render(pad(attribution(m.provider.Kind), m.width)))
	m.content = strings.Join(rows, "\n")
	m.overlay()
}
func (m *viewer) View() tea.View {
	v := tea.NewView(m.content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.BackgroundColor = lipgloss.Color("#182431")
	v.Cursor = m.cursor
	return v
}
func openViewer(path string, a Args) error {
	c, e := viewport(nil, a)
	if e != nil {
		return e
	}
	if path == "" && a.PointIndex != nil {
		return fmt.Errorf("point_index requires a track file")
	}
	p := newProvider(a.Provider)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &viewer{path: path, args: a, provider: p, ctx: ctx, camera: c, width: 120, height: 40, selected: -1, dirty: true, showProfile: true}
	m.initUI()
	m.layout()
	_, e = tea.NewProgram(m).Run()
	if e != nil {
		return e
	}
	return m.fatal
}
