package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"
)

func run() error {
	command, e := parseCLI(os.Args[1:], os.Stdout)
	if e == flag.ErrHelp {
		return nil
	}
	if e != nil {
		return e
	}
	if command.Name == "mcp" {
		return serveMCP()
	}
	ctx := context.Background()
	if command.Name == "auth" || command.Name == "render" {
		var cancel context.CancelFunc
		ctx, cancel = signal.NotifyContext(ctx, os.Interrupt)
		defer cancel()
	}
	if command.Name == "auth" {
		return runAuth(ctx, command, authStreams{in: os.Stdin, out: os.Stdout, err: os.Stderr})
	}
	a := command.Args
	if command.Name == "open" {
		return openViewerFiles(command.Paths, a)
	}
	start := time.Now()
	var t *Track
	if command.Path != "" && command.Name != "render" {
		d, err := loadDocument(command.Path)
		if err != nil {
			return err
		}
		if command.Items {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"filename": d.Filename, "items": d.itemInfos()})
		}
		t, e = d.selectedTrack(a.ItemID)
		if e != nil {
			return e
		}
	}
	loadMs := float64(time.Since(start).Microseconds()) / 1000
	var result any
	switch command.Name {
	case "summary":
		result = t.summary()
	case "inspect":
		result, e = t.inspect(a.PointIndex, a.Distance)
	case "render":
		var credential renderingCredential
		kind := a.Provider
		if !a.ProviderSet {
			kind = ""
		}
		if kind != "none" {
			credential = resolveCredential(ctx)
		}
		provider := providerForCredential(kind, credential)
		provider.quiet = kind == "none"
		result, e = renderFiles(
			ctx,
			command.Paths,
			a,
			provider,
		)
		if e == nil && !command.JSON {
			r := result.(map[string]any)
			fmt.Println(r["text"])
			if notice := providerConnectionNotice(provider); notice != "" {
				fmt.Println(notice)
			} else {
				fmt.Println(r["scene"].(map[string]any)["attribution"])
			}
			if status := r["scene"].(map[string]any)["status"].(string); status != "" {
				fmt.Fprintln(os.Stderr, "warning:", status)
			}
			return nil
		}
	case "bench":
		if command.Frames < 1 {
			return fmt.Errorf("frames must be positive")
		}
		c, err := viewport(t, a)
		if err != nil {
			return err
		}
		features := benchmarkFeatures(t)
		samples := []float64{}
		start = time.Now()
		for i := 0; i < command.Frames; i++ {
			now := time.Now()
			c.pan(0.2, 0)
			g := render(t, c, features, -1, a.ASCII)
			_ = g.plain(a.ASCII)
			samples = append(samples, float64(time.Since(now).Nanoseconds())/1e6)
		}
		result = map[string]any{"load_ms": loadMs, "frames": command.Frames, "total_ms": float64(time.Since(start).Nanoseconds()) / 1e6, "frame_ms": samples, "point_count": len(t.Points)}
	default:
		return fmt.Errorf("unknown mode %q", command.Name)
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
