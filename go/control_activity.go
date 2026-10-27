package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const controlActivityDuration = 2 * time.Second
const controlActivityLimit = 5
const controlActivityGap = 1

type controlActionLabel struct {
	running, complete, subject string
}

var controlActionLabels = map[string]controlActionLabel{
	"load":             {running: "Loading track", complete: "Track loaded", subject: "Track import"},
	"select":           {running: "Selecting item", complete: "Item selected", subject: "Selection"},
	"visibility":       {running: "Updating visibility", complete: "Visibility updated", subject: "Visibility change"},
	"remove":           {running: "Removing item", complete: "Item removed", subject: "Item removal"},
	"workspace.clear":  {running: "Clearing workspace", complete: "Workspace cleared", subject: "Workspace clear"},
	"workspace.open":   {running: "Opening workspace", complete: "Workspace opened", subject: "Workspace open"},
	"workspace.save":   {running: "Saving workspace", complete: "Workspace saved", subject: "Workspace save"},
	"camera.set":       {running: "Moving camera", complete: "Camera updated", subject: "Camera move"},
	"camera.pan":       {running: "Moving camera", complete: "Camera updated", subject: "Camera move"},
	"camera.fit":       {running: "Fitting map", complete: "Map fitted", subject: "Map fit"},
	"camera.animation": {running: "Updating animation", complete: "Animation updated", subject: "Animation change"},
	"playback.play":    {running: "Starting playback", complete: "Playback started", subject: "Playback start"},
	"playback.pause":   {running: "Pausing playback", complete: "Playback paused", subject: "Playback pause"},
	"playback.seek":    {running: "Seeking recording", complete: "Playback updated", subject: "Playback seek"},
	"playback.speed":   {running: "Changing speed", complete: "Playback speed updated", subject: "Speed change"},
	"journey.create":   {running: "Creating journey", complete: "Journey created", subject: "Journey creation"},
	"journey.reverse":  {running: "Reversing journey", complete: "Journey reversed", subject: "Journey reversal"},
	"journey.mode": {
		running: "Changing travel mode", complete: "Travel mode updated", subject: "Travel mode change",
	},
	"journey.stops":       {running: "Updating stops", complete: "Stops updated", subject: "Stop changes"},
	"journey.stop.add":    {running: "Adding stop", complete: "Stop added", subject: "Stop addition"},
	"journey.stop.set":    {running: "Updating stop", complete: "Stop updated", subject: "Stop change"},
	"journey.stop.remove": {running: "Removing stop", complete: "Stop removed", subject: "Stop removal"},
	"journey.stop.move":   {running: "Moving stop", complete: "Stop moved", subject: "Stop move"},
	"journey.calculate":   {running: "Calculating route", complete: "Route calculated", subject: "Route calculation"},
	"journey.show":        {running: "Showing journey", complete: "Journey shown", subject: "Journey display"},
	"operation.cancel":    {running: "Cancelling command", complete: "Cancellation checked", subject: "Cancellation"},
	"theme.set":           {running: "Changing theme", complete: "Theme updated", subject: "Theme change"},
}

type controlActivity struct {
	entries []controlNotification
	text    string
}

type controlNotification struct {
	requestID      string
	action         string
	state          string
	conflict       bool
	routingFailure bool
	retained       bool
	expires        time.Time
}

func controlLabel(action string) controlActionLabel {
	if label, ok := controlActionLabels[action]; ok {
		return label
	}
	return controlActionLabel{running: "Running command", complete: "Command completed", subject: "Command"}
}

func (m *viewer) receiveControlActivity(request controlRequest, response controlResponse) {
	if controlRead(request.Action) {
		return
	}
	op := m.control.operations[request.ID]
	retained := op != nil && (response.Error == nil || response.Error.Code != "request_id_conflict")
	if retained && op.activitySeen {
		return
	}
	now := time.Now()
	m.reconcileOperations()
	if retained {
		op.activitySeen = true
	}
	notice := controlNotification{requestID: request.ID, action: request.Action, retained: retained}
	notice.update(response, now)
	a := &m.control.activity
	a.entries = append([]controlNotification{notice}, a.entries...)
	m.updateControlActivity(now)
}

func (n *controlNotification) update(response controlResponse, now time.Time) {
	n.state = response.State
	n.conflict = response.Error != nil && strings.HasSuffix(response.Error.Code, "_conflict")
	n.routingFailure = response.Error != nil &&
		(response.Error.Code == "routing_failed" || response.Error.Code == "routing_unavailable")
	n.expires = now.Add(controlActivityDuration)
}

func (n controlNotification) label(op *controlOperation, now time.Time, ascii, animated bool) string {
	label := controlLabel(n.action)
	routing := n.routingFailure || (op != nil && op.routeID != 0 && n.state == "pending")
	if routing {
		label = controlLabel("journey.calculate")
	}
	if n.state == "pending" {
		text := label.running
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		if ascii {
			frames = []string{"|", "/", "-", "\\"}
		}
		frame := frames[(now.UnixMilli()/80)%int64(len(frames))]
		if !animated {
			frame = "*"
		}
		return frame + " Remote: " + text
	}
	text := label.complete
	switch n.state {
	case "failed":
		text = label.subject + " failed"
		if n.conflict {
			text = label.subject + " conflicted"
		}
	case "cancelled":
		text = label.subject + " cancelled"
	case "superseded":
		text = label.subject + " replaced"
	}
	return "Remote: " + text
}

func (m *viewer) updateControlActivity(now time.Time) {
	if m.control == nil {
		return
	}
	a := &m.control.activity
	entries := a.entries[:0]
	var labels []string
	step := controlActivityGap + 1
	limit := min(controlActivityLimit, (m.camera.Height+step)/step)
	for _, notice := range a.entries {
		var op *controlOperation
		if notice.retained {
			op = m.control.operations[notice.requestID]
			if op != nil && op.response.State != notice.state {
				notice.update(op.response, now)
			}
		}
		if notice.state != "pending" && !now.Before(notice.expires) {
			continue
		}
		entries = append(entries, notice)
		if len(labels) < limit {
			labels = append(labels, notice.label(op, now, m.settings.ASCII, !m.settings.NoAnimation))
		}
	}
	clear(a.entries[len(entries):])
	a.entries = entries
	if len(entries) > limit {
		labels[limit-1] = fmt.Sprintf("Remote: +%d earlier commands", len(entries)-limit+1)
	}
	text := strings.Join(labels, "\n")
	if text != a.text {
		a.text = text
		m.dirty = true
	}
}

func (m *viewer) controlActivityRow(y int) (string, int) {
	if m.control == nil || m.control.activity.text == "" || m.width < 30 || m.height < 12 {
		return "", 0
	}
	rows := strings.Split(m.control.activity.text, "\n")
	step := controlActivityGap + 1
	outside := y < 0 || y > m.camera.Height || y%step != 0
	if outside {
		return "", 0
	}
	index := y / step
	if index >= len(rows) {
		return "", 0
	}
	ellipsis := "…"
	if m.settings.ASCII {
		ellipsis = "..."
	}
	label := " " + ansi.Truncate(rows[index], m.width-6, ellipsis) + " "
	return label, m.width - ansi.StringWidth(label) - 2
}

func (m *viewer) overlayControlActivity() {
	if m.control == nil || m.control.activity.text == "" {
		return
	}
	rows := strings.Split(m.content, "\n")
	t := m.theme()
	style := lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.Color(t.Background)).Background(lipgloss.Color(t.Text))
	for y := 0; y < len(rows); y += controlActivityGap + 1 {
		label, x := m.controlActivityRow(y)
		if label == "" {
			break
		}
		rows[y] = ansi.Cut(rows[y], 0, x) + style.Render(label) +
			ansi.Cut(rows[y], m.width-2, m.width)
	}
	m.content = strings.Join(rows, "\n")
}
