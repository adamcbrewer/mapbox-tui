package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatchMapboxCallbacksStopsWithoutConsumingLaterInput(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	var output bytes.Buffer
	stop, err := watchMapboxCallbacks(t.Context(), authStreams{in: input, err: &output})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("callback reader did not stop when login finished")
	}
	if strings.Contains(output.String(), "unavailable") {
		t.Fatal("normal browser completion reported a callback input failure")
	}
	if _, err := io.WriteString(writer, "next viewer input\n"); err != nil {
		t.Fatal(err)
	}
	value := make([]byte, len("next viewer input\n"))
	if _, err := io.ReadFull(input, value); err != nil || string(value) != "next viewer input\n" {
		t.Fatal("callback reader closed or consumed subsequent terminal input", err)
	}
}

func TestReadMapboxCallbacksRetriesInvalidInputAndPreservesAuthorization(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/callback" {
			t.Error("callback was not forwarded as a GET to /callback")
		}
		received <- r.URL.Query().Get("code") + ":" + r.URL.Query().Get("state")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	callback := strings.Replace(server.URL, "127.0.0.1", "localhost", 1) +
		"/callback?code=sensitive%2Bcode%2Fvalue&state=sensitive-state"
	input := strings.NewReader("\nhttps://example.com/?code=do-not-print\n" + callback + "\n")
	var output bytes.Buffer
	readMapboxCallbacks(t.Context(), input, &output)
	select {
	case value := <-received:
		if value != "sensitive+code/value:sensitive-state" {
			t.Fatal("callback changed the code or state")
		}
	default:
		t.Fatal("valid callback was not forwarded after invalid input")
	}
	if !strings.Contains(output.String(), "waiting for Mapbox CLI to confirm login") {
		t.Fatal("delivery was not distinguished from successful authentication")
	}
	for _, secret := range []string{"sensitive", "do-not-print"} {
		if strings.Contains(output.String(), secret) {
			t.Fatal("callback input leaked into output")
		}
	}
}

func TestRelayMapboxCallbackRejectsNonCallbackURLs(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, test := range []struct {
		name, value string
	}{
		{name: "external host", value: "http://example.com:1234/callback?code=secret&state=secret"},
		{name: "localhost suffix", value: "http://localhost.example.com:1234/callback?code=secret&state=secret"},
		{name: "https", value: strings.Replace(server.URL, "http:", "https:", 1) + "/callback?code=secret&state=secret"},
		{name: "userinfo", value: strings.Replace(server.URL, "http://", "http://secret@", 1) + "/callback?code=secret&state=secret"},
		{name: "wrong path", value: server.URL + "/other?code=secret&state=secret"},
		{name: "encoded path", value: server.URL + "/%63allback?code=secret&state=secret"},
		{name: "fragment", value: server.URL + "/callback?code=secret&state=secret#secret"},
		{name: "missing port", value: "http://localhost/callback?code=secret&state=secret"},
		{name: "zero port", value: "http://localhost:0/callback?code=secret&state=secret"},
		{name: "large port", value: "http://localhost:65536/callback?code=secret&state=secret"},
		{name: "malformed port", value: "http://localhost:secret/callback?code=secret&state=secret"},
		{name: "missing state", value: server.URL + "/callback?code=secret"},
		{name: "empty code", value: server.URL + "/callback?code=&state=secret"},
		{name: "duplicate code", value: server.URL + "/callback?code=secret&code=secret&state=secret"},
		{name: "duplicate state", value: server.URL + "/callback?code=secret&state=secret&state=secret"},
		{name: "extra parameter", value: server.URL + "/callback?code=secret&state=secret&error=secret"},
		{name: "invalid escape", value: server.URL + "/callback?code=%secret&state=secret"},
		{name: "oversized", value: server.URL + "/callback?code=" + strings.Repeat("secret", 1000) + "&state=secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := relayMapboxCallback(t.Context(), test.value)
			if err == nil {
				t.Fatal("invalid callback was accepted")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("rejected callback leaked into the error")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("invalid callback made a network request")
	}
}

func TestRelayMapboxCallbackDoesNotFollowRedirects(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/?code=secret&state=secret", http.StatusFound)
	}))
	defer server.Close()
	err := relayMapboxCallback(t.Context(), server.URL+"/callback?code=secret&state=secret")
	if err == nil || leaked.Load() || strings.Contains(err.Error(), "secret") {
		t.Fatal("redirect was followed or leaked callback data", err)
	}
}

func TestRelayMapboxCallbackFailureAndCancellationDoNotLeakURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()
	callback := server.URL + "/callback?code=sensitive-code&state=sensitive-state"
	for _, cancelled := range []bool{false, true} {
		name := "listener unavailable"
		if cancelled {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelled {
				cancel()
			}
			err := relayMapboxCallback(ctx, callback)
			if err == nil || strings.Contains(err.Error(), "sensitive") {
				t.Fatal("callback failure was missing or exposed the URL", err)
			}
		})
	}
}
