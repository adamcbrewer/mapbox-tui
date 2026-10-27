package main

import (
	"fmt"
	"image/color"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func activityControl(m *viewer, request controlRequest) (controlResponse, tea.Cmd) {
	reply := make(chan controlResponse, 1)
	cmd := m.handleControl(controlMessage{request: request, reply: reply})
	return <-reply, cmd
}

func TestControlActionLabelsCoverMutations(t *testing.T) {
	for action := range controlSpecs {
		t.Run(action, func(t *testing.T) {
			label, exists := controlActionLabels[action]
			if controlRead(action) {
				if exists {
					t.Fatal("background read has an activity label")
				}
				return
			}
			if !exists || label.running == "" || label.complete == "" || label.subject == "" {
				t.Fatal("mutation needs human-readable running, completion and outcome labels")
			}
			if strings.Contains(label.running+label.complete+label.subject, ".") {
				t.Fatal("label exposes a command identifier", label)
			}
		})
	}
	for action := range controlActionLabels {
		if _, exists := controlSpecs[action]; !exists {
			t.Fatal("activity label has no matching command", action)
		}
	}
}

func TestControlActivityAcknowledgesImmediatelyAndExpiresWithoutRetryReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := journeyViewer(t)
		request := controlTestRequest(m, "camera.set", map[string]any{"zoom": 12, "animate": false})
		response, _ := activityControl(m, request)
		if response.State != "applied" || m.camera.Zoom != 12 {
			t.Fatal("feedback delayed the camera change", response)
		}
		if m.control.activity.text != "Remote: Camera updated" {
			t.Fatal("missing immediate acknowledgement", m.control.activity.text)
		}
		expires, revision := m.control.activity.entries[0].expires, m.workspace.liveRevision
		time.Sleep(time.Second)
		for _, action := range []string{"status", "capabilities", "operation.status", "theme.list"} {
			args := map[string]any{}
			if action == "operation.status" {
				args["operation_id"] = request.ID
			}
			activityControl(m, controlTestRequest(m, action, args))
		}
		duplicate, _ := activityControl(m, request)
		if !reflect.DeepEqual(response, duplicate) || m.control.activity.entries[0].expires != expires {
			t.Fatal("poll or replay changed the command result or notification lifetime")
		}
		time.Sleep(time.Second - time.Nanosecond)
		m.updateControlActivity(time.Now())
		if m.control.activity.text == "" {
			t.Fatal("notification disappeared before two seconds")
		}
		time.Sleep(time.Nanosecond)
		m.updateControlActivity(time.Now())
		if m.control.activity.text != "" || m.workspace.liveRevision != revision {
			t.Fatal("notification did not expire cleanly", m.control.activity.text)
		}
		activityControl(m, request)
		if m.control.activity.text != "" {
			t.Fatal("replay revived expired feedback")
		}
	})
}

func TestControlActivityPendingOverlapAndFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := journeyViewer(t)
		m.settings.NoAnimation = false
		t.Cleanup(m.cancelControlOperations)
		var requests []controlRequest
		var commands []tea.Cmd
		for _, path := range []string{"example.gpx", "waypoints.gpx"} {
			absolute, err := filepath.Abs(filepath.Join("../fixtures", path))
			if err != nil {
				t.Fatal(err)
			}
			request := controlTestRequest(m, "load", map[string]any{"path": absolute})
			response, cmd := activityControl(m, request)
			if response.State != "pending" || cmd == nil {
				t.Fatal("import was not pending", response)
			}
			requests, commands = append(requests, request), append(commands, cmd)
		}
		if strings.Count(m.control.activity.text, "Loading track") != 2 {
			t.Fatal("overlapping commands did not get separate notifications", m.control.activity.text)
		}
		m.Update(commands[1]())
		m.updateControlActivity(time.Now())
		rows := strings.Split(m.control.activity.text, "\n")
		if rows[0] != "Remote: Track loaded" || !strings.Contains(rows[1], "Loading track") {
			t.Fatal("completion replaced the unfinished command", rows)
		}
		time.Sleep(3 * time.Second)
		m.updateControlActivity(time.Now())
		if !strings.Contains(m.control.activity.text, "Loading track") {
			t.Fatal("completion hid an unfinished command", m.control.activity.text)
		}
		before := m.control.activity.text
		time.Sleep(80 * time.Millisecond)
		m.updateControlActivity(time.Now())
		if m.control.activity.text == before {
			t.Fatal("pending spinner did not advance")
		}
		m.Update(controlLoaded{id: requests[0].ID, err: fmt.Errorf("unavailable file")})
		m.updateControlActivity(time.Now())
		if m.control.activity.text != "Remote: Track import failed" {
			t.Fatal("failed operation left a busy spinner", m.control.activity.text)
		}
		time.Sleep(2 * time.Second)
		m.updateControlActivity(time.Now())
		if m.control.activity.text != "" {
			t.Fatal("failure did not expire")
		}
	})
}

func TestControlActivityConflictsCancellationAndSupersession(t *testing.T) {
	for _, outcome := range []string{"conflict", "cancel", "replace"} {
		t.Run(outcome, func(t *testing.T) {
			m := journeyViewer(t)
			request := controlTestRequest(m, "camera.set", map[string]any{"zoom": 12, "animate": true})
			if outcome == "conflict" {
				m.workspace.edited()
				activityControl(m, request)
				if m.control.activity.text != "Remote: Camera move conflicted" {
					t.Fatal("conflict missing from feedback", m.control.activity.text)
				}
				return
			}
			response, _ := activityControl(m, request)
			if response.State != "pending" {
				t.Fatal("camera animation did not start", response)
			}
			if outcome == "cancel" {
				cancel := controlTestRequest(m, "operation.cancel", map[string]any{"operation_id": request.ID})
				activityControl(m, cancel)
				rows := strings.Split(m.control.activity.text, "\n")
				if rows[0] != "Remote: Cancellation checked" || rows[1] != "Remote: Camera move cancelled" {
					t.Fatal("cancellation stayed busy", m.control.activity.text)
				}
			} else {
				activityControl(m, controlTestRequest(m, "camera.set", map[string]any{"zoom": 9, "animate": false}))
				m.reconcileOperations()
				m.updateControlActivity(time.Now())
				rows := strings.Split(m.control.activity.text, "\n")
				if rows[0] != "Remote: Camera updated" || rows[1] != "Remote: Camera move replaced" {
					t.Fatal("older supersession replaced newer feedback", m.control.activity.text)
				}
			}
		})
	}
}

func TestControlActivityHumanInterruption(t *testing.T) {
	m := journeyViewer(t)
	request := controlTestRequest(m, "camera.set", map[string]any{"zoom": 12, "animate": true})
	activityControl(m, request)
	m.panCamera(1, 0)
	m.reconcileOperations()
	m.updateControlActivity(time.Now())
	if m.control.activity.text != "Remote: Camera move replaced" {
		t.Fatal("human interruption was reported as success", m.control.activity.text)
	}
	if m.control.operations[request.ID].response.State != "superseded" {
		t.Fatal("human navigation did not supersede the operation")
	}
}

func TestControlActivityRoutingPhaseAndCompletion(t *testing.T) {
	m := journeyViewer(t)
	m.preferences.BillingAcceptedVersion = billingNoticeVersion
	m.preferences.Offline = false
	m.directions = newDirectionsClient("token")
	m.directions.client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		return locationResponse(directionsTestResponse(directionsRequestLocations(t, r), 12345, 3661)), nil
	})
	request := controlTestRequest(m, "journey.create", map[string]any{
		"mode": "cycling", "stops": []controlStop{{Lat: 51.5, Lon: -0.1}, {Lat: 51.6, Lon: -0.2}},
	})
	response, cmd := activityControl(m, request)
	if response.State != "pending" || cmd == nil {
		t.Fatal("journey did not start routing", response)
	}
	m.updateControlActivity(time.Now())
	if !strings.Contains(m.control.activity.text, "Calculating route") {
		t.Fatal("pending route had no readable phase label", m.control.activity.text)
	}
	m.Update(cmd())
	m.updateControlActivity(time.Now())
	if m.control.activity.text != "Remote: Journey created" {
		t.Fatal("routing did not complete the original command", m.control.activity.text)
	}
}

func TestControlActivityRoutingFailurePreservesAppliedEdit(t *testing.T) {
	m := journeyViewer(t)
	m.preferences.BillingAcceptedVersion = billingNoticeVersion
	m.preferences.Offline = false
	m.directions = newDirectionsClient("token")
	m.directions.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("directions unavailable")
	})
	request := controlTestRequest(m, "journey.create", map[string]any{
		"mode": "cycling", "stops": []controlStop{{Lat: 51.5, Lon: -0.1}, {Lat: 51.6, Lon: -0.2}},
	})
	response, cmd := activityControl(m, request)
	if response.State != "pending" || !response.Changed || cmd == nil {
		t.Fatal("journey edit was not applied before routing", response)
	}
	m.Update(cmd())
	m.updateControlActivity(time.Now())
	op := m.control.operations[request.ID]
	if op.response.State != "failed" || !op.response.Changed || len(m.workspace.documents) != 1 {
		t.Fatal("failed routing discarded the journey", op.response)
	}
	if m.control.activity.text != "Remote: Route calculation failed" {
		t.Fatal("routing failure misreported the applied edit", m.control.activity.text)
	}
}

func TestControlActivityRoutingPreflightFailurePreservesAppliedEdit(t *testing.T) {
	for _, failure := range []string{"offline", "unacknowledged", "disconnected", "distance"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
			m := journeyViewer(t)
			m.preferences.BillingAcceptedVersion = billingNoticeVersion
			m.preferences.Offline = false
			m.directions = newDirectionsClient("token")
			m.directions.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("routing preflight made a directions request")
				return nil, nil
			})
			stops := []controlStop{{Lat: 51.5, Lon: -0.1}, {Lat: 51.6, Lon: -0.2}}
			code := "routing_unavailable"
			switch failure {
			case "offline":
				m.preferences.Offline = true
			case "unacknowledged":
				m.preferences.BillingAcceptedVersion = 0
			case "disconnected":
				m.directions = nil
			case "distance":
				stops[1] = controlStop{Lat: 80, Lon: 150}
				code = "routing_failed"
			}
			request := controlTestRequest(m, "journey.create", map[string]any{"mode": "walking", "stops": stops})
			response, cmd := activityControl(m, request)
			if response.State != "failed" || response.Error == nil || response.Error.Code != code ||
				!response.Changed || cmd != nil || m.control.operations[request.ID].routeID != 0 {
				t.Fatal("expected a retained edit with no route request", response)
			}
			if _, err := m.workspace.journey(controlTestRef(t, response)); err != nil {
				t.Fatal("routing preflight discarded the journey", err)
			}
			if m.control.activity.text != "Remote: Route calculation failed" {
				t.Fatal("routing preflight misreported the applied edit", m.control.activity.text)
			}
		})
	}
}

func TestControlActivityCancellationOfCompletedOperation(t *testing.T) {
	m := journeyViewer(t)
	request := controlTestRequest(m, "camera.set", map[string]any{"zoom": 12, "animate": false})
	response, _ := activityControl(m, request)
	activityControl(m, controlTestRequest(m, "operation.cancel", map[string]any{"operation_id": request.ID}))
	if !reflect.DeepEqual(m.control.operations[request.ID].response, response) {
		t.Fatal("cancellation changed an already completed operation")
	}
	if m.control.activity.text != "Remote: Cancellation checked\nRemote: Camera updated" {
		t.Fatal("cancellation claimed completed work was cancelled", m.control.activity.text)
	}
}

func TestControlActivityQueuedPulseKeepsCompletionVisible(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := journeyViewer(t)
		t.Cleanup(m.cancelControlOperations)
		request := controlTestRequest(m, "load", map[string]any{"path": "/unavailable.gpx"})
		activityControl(m, request)
		queued := pulse(time.Now())
		time.Sleep(3 * time.Second)
		m.Update(controlLoaded{id: request.ID, err: fmt.Errorf("unavailable file")})
		m.Update(queued)
		if m.control.activity.entries[0].expires != time.Now().Add(controlActivityDuration) {
			t.Fatal("completion timer included time spent waiting in the event queue")
		}
		time.Sleep(time.Second)
		m.Update(pulse(time.Now()))
		if m.control.activity.text != "Remote: Track import failed" {
			t.Fatal("completion disappeared before two visible seconds", m.control.activity.text)
		}
		time.Sleep(time.Second)
		m.Update(pulse(time.Now()))
		if m.control.activity.text != "" {
			t.Fatal("completion did not expire after two visible seconds")
		}
	})
}

func TestControlActivityWorkspaceSaveRendersBeforeWritingAndRepliesOnce(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := journeyViewer(t)
	request := controlTestRequest(m, "workspace.save", map[string]any{"name": "Deferred save"})
	reply := make(chan controlResponse, 1)
	cmd := m.handleControl(controlMessage{request: request, reply: reply})
	if cmd == nil || len(reply) != 0 || m.workspace.checkpoint != nil {
		t.Fatal("save wrote or replied before rendering its running frame")
	}
	if !strings.Contains(m.View().Content, "Saving workspace") {
		t.Fatal("pending save was not rendered")
	}
	retry, retryCmd := activityControl(m, request)
	if retry.State != "pending" || retryCmd != nil || len(m.control.activity.entries) != 1 {
		t.Fatal("pending retry started another save or notification", retry)
	}
	revision := m.workspace.liveRevision
	start := cmd()
	m.Update(start)
	response := <-reply
	if response.State != "applied" || !response.Changed || response.OperationID != request.ID {
		t.Fatal("save did not reply with its final outcome", response)
	}
	if m.workspace.liveRevision != revision+1 || m.workspace.modified(m.destination(), m.planner.ref) {
		t.Fatal("save did not record exactly one clean checkpoint")
	}
	if controlTestResult(t, response)["path"] != m.workspace.checkpoint.path ||
		m.control.activity.text != "Remote: Workspace saved" {
		t.Fatal("save lost its checkpoint path or completion label", response)
	}
	m.Update(start)
	duplicate, _ := activityControl(m, request)
	if len(reply) != 0 || m.workspace.liveRevision != revision+1 || !reflect.DeepEqual(response, duplicate) {
		t.Fatal("save start or retry wrote or replied twice")
	}
}

func TestControlActivityWorkspaceSaveRejectsChangesBeforeStart(t *testing.T) {
	for _, change := range []string{"edit", "clear", "cancel"} {
		t.Run(change, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			m := journeyViewer(t)
			request := controlTestRequest(m, "workspace.save", map[string]any{"name": "Do not save"})
			reply := make(chan controlResponse, 1)
			cmd := m.handleControl(controlMessage{request: request, reply: reply})
			if cmd == nil {
				t.Fatal("save did not defer its filesystem write")
			}
			want := "superseded"
			switch change {
			case "edit":
				m.workspace.edited()
			case "clear":
				m.clearWorkspace()
			case "cancel":
				activityControl(m, controlTestRequest(m, "operation.cancel", map[string]any{"operation_id": request.ID}))
				want = "cancelled"
			}
			m.Update(cmd())
			response := <-reply
			store, err := localWorkspaceStore()
			if err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(store.directory, "*.json"))
			if err != nil {
				t.Fatal(err)
			}
			if response.State != want || response.Changed || m.workspace.checkpoint != nil || len(files) != 0 {
				t.Fatal("stale or cancelled save wrote a checkpoint", response, files)
			}
		})
	}
}

func TestControlActivityStackKeepsArrivalOrderAndIndependentExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := journeyViewer(t)
		first := controlTestRequest(m, "camera.animation", map[string]any{"enabled": false})
		activityControl(m, first)
		time.Sleep(400 * time.Millisecond)
		activityControl(m, controlTestRequest(m, "camera.set", map[string]any{"zoom": 12, "animate": false}))
		time.Sleep(400 * time.Millisecond)
		activityControl(m, controlTestRequest(m, "theme.set", map[string]any{"theme": "paper"}))
		want := "Remote: Theme updated\nRemote: Camera updated\nRemote: Animation updated"
		if m.control.activity.text != want {
			t.Fatal("newest command is not at the top", m.control.activity.text)
		}
		activityControl(m, first)
		if m.control.activity.text != want || len(m.control.activity.entries) != 3 {
			t.Fatal("retry reordered or duplicated a notification", m.control.activity.text)
		}
		time.Sleep(1200 * time.Millisecond)
		m.updateControlActivity(time.Now())
		if m.control.activity.text != "Remote: Theme updated\nRemote: Camera updated" {
			t.Fatal("oldest expiry hid newer commands", m.control.activity.text)
		}
		time.Sleep(400 * time.Millisecond)
		m.updateControlActivity(time.Now())
		if m.control.activity.text != "Remote: Theme updated" {
			t.Fatal("notifications did not expire independently", m.control.activity.text)
		}
		time.Sleep(400 * time.Millisecond)
		m.updateControlActivity(time.Now())
		if m.control.activity.text != "" || len(m.control.activity.entries) != 0 {
			t.Fatal("expired stack remained visible")
		}
	})
}

func TestControlActivityStackBoundsOverlayAndRestoresMap(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {30, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := journeyViewer(t)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.settings.Elevation = elevationHide
			m.layout()
			m.rebuild()
			before := strings.Split(m.content, "\n")
			camera, revision := m.camera, m.workspace.liveRevision
			for range 8 {
				activityControl(m, controlTestRequest(m, "unknown.action", map[string]any{}))
			}
			m.rebuild()
			after := strings.Split(m.content, "\n")
			rows := strings.Split(m.control.activity.text, "\n")
			wantRows := 5
			if size[1] == 12 {
				wantRows = 4
			}
			if len(rows) != wantRows || rows[wantRows-1] != fmt.Sprintf("Remote: +%d earlier commands", 8-wantRows+1) {
				t.Fatal("burst was not bounded with an overflow count", rows)
			}
			for y := range before {
				label, left := m.controlActivityRow(y)
				if label != "" {
					if ansi.Strip(ansi.Cut(before[y], 0, left)) != ansi.Strip(ansi.Cut(after[y], 0, left)) {
						t.Fatal("toast changed uncovered map cells", y)
					}
				} else if before[y] != after[y] {
					t.Fatal("toast changed rows outside the stack", y)
				}
			}
			if m.camera != camera || m.workspace.liveRevision != revision ||
				lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) != size[1] {
				t.Fatal("stack changed viewport geometry or workspace state")
			}
			m.updateControlActivity(time.Now().Add(controlActivityDuration))
			m.rebuild()
			if !reflect.DeepEqual(before, strings.Split(m.content, "\n")) {
				t.Fatal("expired notifications did not restore the same map")
			}
		})
	}
}

func TestControlActivityStackCapturesCoveredMouseWithoutStickingDrags(t *testing.T) {
	m := journeyViewer(t)
	for range 2 {
		activityControl(m, controlTestRequest(m, "unknown.action", map[string]any{}))
	}
	m.rebuild()
	_, left := m.controlActivityRow(2)
	x, y := left+1, 2
	before := m.camera
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
	if m.camera != before || m.inspected != nil || m.dragging {
		t.Fatal("notification passed input to the covered map")
	}
	m.Update(tea.MouseClickMsg{X: 2, Y: 5, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if m.dragging || m.rotating || m.inspected != nil || m.camera == before {
		t.Fatal("drag crossing the stack became stuck or inspected a hidden location")
	}
	m.Update(tea.MouseClickMsg{X: x, Y: 1, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: x, Y: 1, Button: tea.MouseLeft})
	if m.inspected == nil {
		t.Fatal("gap between notifications blocked the visible map")
	}
}

func TestControlActivityOverlayKeepsMapGeometryAndThemeContrast(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {70, 24}, {30, 12}} {
		for _, ascii := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/ascii=%t", size[0], size[1], ascii), func(t *testing.T) {
				m := themeViewer(
					t,
					size[0],
					size[1],
					ascii,
				)
				m.settings.Elevation = elevationHide
				m.layout()
				for i := range m.themes {
					m.applyTheme(i)
					m.control = nil
					m.rebuild()
					before := strings.Split(m.content, "\n")
					camera, revision := m.camera, m.workspace.liveRevision
					activityControl(m, controlTestRequest(m, "unknown.action", map[string]any{}))
					m.rebuild()
					after := strings.Split(m.content, "\n")
					if !strings.Contains(ansi.Strip(after[0]), "Remote: Command failed") {
						t.Fatal("missing readable fallback", ansi.Strip(after[0]))
					}
					if !reflect.DeepEqual(before[1:], after[1:]) || m.camera != camera ||
						m.workspace.liveRevision != revision {
						t.Fatal("activity changed map geometry, content or revision")
					}
					if lipgloss.Width(m.content) > size[0] || lipgloss.Height(m.content) != size[1] {
						t.Fatal("activity changed terminal dimensions")
					}
					if themeContrast(m.theme().Text, m.theme().Background) < 4.5 {
						t.Fatal("activity badge has insufficient contrast", m.theme().ID)
					}
					if ascii && strings.Contains(ansi.Strip(after[0]), "…") {
						t.Fatal("ASCII feedback used a Unicode ellipsis")
					}
				}
			})
		}
	}
}

func TestControlActivityStackRecoloursExistingNotificationsWithTheme(t *testing.T) {
	m := journeyViewer(t)
	custom := m.themes[0]
	custom.ID, custom.Name = "custom-toast", "Custom toast"
	custom.Background, custom.Text = "#142b36", "#ffe1ba"
	m.themes = append(m.themes, custom)
	activityControl(m, controlTestRequest(m, "camera.animation", map[string]any{"enabled": false}))
	activityControl(m, controlTestRequest(m, "camera.set", map[string]any{"zoom": 12, "animate": false}))
	before := slices.Clone(m.control.activity.entries)
	for i, theme := range m.themes {
		t.Run(theme.ID, func(t *testing.T) {
			m.applyTheme(i)
			m.rebuild()
			canvas := lipgloss.NewCanvas(m.width, m.height).Compose(
				lipgloss.NewCompositor(lipgloss.NewLayer(m.content)),
			)
			for index := range before {
				y := index * 2
				_, left := m.controlActivityRow(y)
				cell := canvas.CellAt(left+1, y)
				if cell == nil {
					t.Fatal("notification was not rendered", y)
				}
				for _, check := range []struct {
					name string
					got  color.Color
					want string
				}{
					{name: "lettering", got: cell.Style.Fg, want: theme.Background},
					{name: "background", got: cell.Style.Bg, want: theme.Text},
				} {
					if check.got == nil {
						t.Fatal("notification has no themed colour", y, check.name)
					}
					r, g, b, a := check.got.RGBA()
					wantR, wantG, wantB, wantA := lipgloss.Color(check.want).RGBA()
					if [4]uint32{r, g, b, a} != [4]uint32{wantR, wantG, wantB, wantA} {
						t.Fatal("notification retained a previous palette", y, check.name)
					}
				}
			}
			if !reflect.DeepEqual(before, m.control.activity.entries) {
				t.Fatal("theme change reset notification ordering, states or expiry")
			}
		})
	}
}
