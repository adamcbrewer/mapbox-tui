package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func captureSourceResult(t *testing.T, command tea.Cmd) sourceResult {
	t.Helper()
	var result *sourceResult
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, next := range msg {
				run(next)
			}
		case sourceResult:
			result = &msg
		}
	}
	run(command)
	if result == nil {
		t.Fatal("viewer did not request source data")
	}
	return *result
}

func TestViewerRefreshesStationaryDataWhileLoadingFilesAndKeepsImportsOnFailure(t *testing.T) {
	now := time.Now()
	p := newProvider("mapbox")
	p.token, p.now = "synthetic-token", func() time.Time { return now }
	data := lifecycleTile(t)
	fail := false
	p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		if fail {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("private diagnostic"))}, nil
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Cache-Control": {"max-age=2"}},
			Body:       io.NopCloser(bytes.NewReader(data)),
		}, nil
	})
	m := &viewer{
		provider: p,
		ctx:      context.Background(),
		width:    120,
		height:   40,
		camera: fit(
			nil,
			88,
			34,
			2,
		),
		showProfile: true,
	}
	m.initUI()
	t.Cleanup(m.sources.close)
	if _, err := m.workspace.open("../fixtures/example.gpx"); err != nil {
		t.Fatal(err)
	}
	m.layout()
	runLoadCommand(m, m.fetchAt(now))
	selection := m.workspace.selection
	before := m.sources.view().Stats.Requests
	if before == 0 {
		t.Fatal("initial basemap did not load")
	}
	m.loading = true
	now = now.Add(3 * time.Second)
	_, cmd := m.Update(pulse(now))
	runLoadCommand(m, cmd)
	if m.sources.view().Stats.Requests <= before || m.workspace.selection != selection {
		t.Fatal("stationary refresh was blocked by file loading or reset selection")
	}
	usable := m.sources.view()
	m.loading = false
	fail = true
	now = now.Add(3 * time.Second)
	_, cmd = m.Update(pulse(now))
	runLoadCommand(m, cmd)
	m.rebuild()
	failureLostImports := m.workspace.selection != selection ||
		len(m.workspace.items()) != 4 ||
		!strings.Contains(m.sources.view().status(), "503") ||
		!strings.Contains(ansi.Strip(m.content), "Riverside exploration")
	if failureLostImports {
		t.Fatal("provider failure lost imported geometry or source status")
	}
	failed := m.sources.view()
	if !reflect.DeepEqual(failed.Features, usable.Features) || !reflect.DeepEqual(failed.Credits, usable.Credits) {
		t.Fatal("failed refresh discarded the last usable basemap or its attribution")
	}
	if failed.Stats.Requests <= usable.Stats.Requests || failed.Stats.Errors <= usable.Stats.Errors {
		t.Fatal("retaining the basemap hid refresh failure statistics")
	}
	if failed.Sources[0].RefreshAt == nil || !failed.Sources[0].RefreshAt.Equal(now.Add(10*time.Second)) {
		t.Fatal("retaining the basemap discarded the provider retry deadline")
	}
}

func TestViewerRejectsResultsForReplacedProviderBeforePolling(t *testing.T) {
	data := lifecycleTile(t)
	provider := func() *Provider {
		p := newProvider("mapbox")
		p.token = "synthetic-token"
		p.client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Cache-Control": {"max-age=60"}},
				Body:       io.NopCloser(bytes.NewReader(data)),
			}, nil
		})
		return p
	}
	m := &viewer{
		provider: provider(),
		ctx:      context.Background(),
		width:    120,
		height:   40,
		camera: fit(
			nil,
			88,
			34,
			2,
		),
	}
	m.initUI()
	t.Cleanup(m.sources.close)
	old := captureSourceResult(t, m.fetch())
	m.provider = provider()
	m.Update(old)
	if len(m.sources.view().Features) != 0 {
		t.Fatal("old provider result was applied after its replacement")
	}
	runLoadCommand(m, m.fetch())
	if len(m.sources.view().Features) == 0 {
		t.Fatal("replacement provider did not load")
	}
	m.Update(old)
	if m.sources.view().Stats.Requests != 1 {
		t.Fatal("old result overwrote replacement provider stats")
	}
}

func TestViewerImportShowsConnectionNoticeAfterLoading(t *testing.T) {
	t.Setenv("MAPBOX_ACCESS_TOKEN", "")
	m := &viewer{
		provider: newProvider("none"),
		loading:  true,
		width:    120,
		height:   40,
		camera: fit(
			nil,
			88,
			34,
			2,
		),
	}
	m.initUI()
	m.rebuild()
	if strings.Contains(m.content, "MAPBOX_ACCESS_TOKEN") {
		t.Fatal("local file loading showed a basemap setup warning")
	}
	if strings.Contains(m.content, mapboxDisconnectedNotice) {
		t.Fatal("connection notice appeared before the local file loaded")
	}
	document, err := loadDocument("../fixtures/example.gpx")
	if err != nil {
		t.Fatal(err)
	}
	m.workspace.addDocument(document)
	m.loading = false
	m.layout()
	m.rebuild()
	if !strings.Contains(m.content, mapboxDisconnectedNotice) {
		t.Fatal("imported geometry hid the missing Mapbox connection")
	}
	if m.echoConnectionNotice() == nil || m.echoConnectionNotice() != nil {
		t.Fatal("connection notice was not scheduled exactly once")
	}
	m.provider.quiet = true
	m.rebuild()
	if strings.Contains(m.content, mapboxDisconnectedNotice) {
		t.Fatal("deliberate offline viewing showed an account warning")
	}
}
