package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
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
	a := command.Args
	if command.Name == "open" {
		return openViewer(command.Path, a)
	}
	start := time.Now()
	var t *Track
	if command.Path != "" {
		t, e = loadTrack(command.Path)
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
		result, e = renderResult(context.Background(), t, a, newProvider(a.Provider))
		if e == nil && !command.JSON {
			r := result.(map[string]any)
			fmt.Println(r["text"])
			fmt.Println(attribution(a.Provider))
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
