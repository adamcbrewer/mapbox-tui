package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/muesli/cancelreader"
)

const mapboxCallbackLimit = 4096

func watchMapboxCallbacks(ctx context.Context, streams authStreams) (func(), error) {
	reader, err := cancelreader.NewReader(streams.in)
	if err != nil {
		return nil, fmt.Errorf("prepare Mapbox callback input: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	fmt.Fprintln(streams.err, "Using another device? Open the Mapbox login URL there, then paste the failed")
	fmt.Fprintln(streams.err, "http://localhost:PORT/callback URL here and press Enter. Keep this login running.")
	var workers sync.WaitGroup
	workers.Go(func() { readMapboxCallbacks(ctx, reader, streams.err) })
	return func() {
		cancel()
		reader.Cancel()
		workers.Wait()
		reader.Close()
	}, nil
}

func readMapboxCallbacks(ctx context.Context, input io.Reader, output io.Writer) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 256), mapboxCallbackLimit+1)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		value := strings.TrimSpace(scanner.Text())
		if value == "" {
			continue
		}
		if err := relayMapboxCallback(ctx, value); err != nil {
			fmt.Fprintln(output, err)
			continue
		}
		fmt.Fprintln(output, "Callback delivered; waiting for Mapbox CLI to confirm login.")
		return
	}
	if scanner.Err() != nil && ctx.Err() == nil {
		fmt.Fprintln(output, "Callback input unavailable or too long; finish in the browser or restart login.")
	}
}

func relayMapboxCallback(ctx context.Context, value string) error {
	u, err := url.Parse(value)
	if err != nil || len(value) > mapboxCallbackLimit {
		return fmt.Errorf("invalid callback URL; paste the complete failed localhost redirect")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"
	plainCallback := u.Scheme == "http" && u.User == nil && u.EscapedPath() == "/callback" && u.Fragment == ""
	port, portErr := strconv.ParseUint(u.Port(), 10, 16)
	if !local || !plainCallback || portErr != nil || port == 0 {
		return fmt.Errorf("expected http://localhost:PORT/callback from the waiting Mapbox login")
	}
	query, err := url.ParseQuery(u.RawQuery)
	oneCodeAndState := len(query["code"]) == 1 && len(query["state"]) == 1
	if err != nil || len(query) != 2 || !oneCodeAndState {
		return fmt.Errorf("callback must contain one authorization code and state; copy the complete redirect URL")
	}
	if query.Get("code") == "" || query.Get("state") == "" {
		return fmt.Errorf("callback authorization code and state must not be empty")
	}
	u.Host = net.JoinHostPort("127.0.0.1", strconv.FormatUint(port, 10))
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("cannot prepare the Mapbox callback")
	}
	client := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		Timeout:   5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot deliver callback; keep the original login running on this machine, or restart it")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("callback listener rejected the request; check the original login or restart it")
	}
	return nil
}
