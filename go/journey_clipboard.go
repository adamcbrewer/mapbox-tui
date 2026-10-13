package main

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *viewer) journeyCommand() (string, error) {
	j, err := m.workspace.journey(m.planner.ref)
	if err != nil {
		return "", err
	}
	args := []string{"mapbox-tui", "--mode=" + strings.ToLower(j.Mode.label())}
	for _, entry := range m.workspace.documents {
		if entry.document.Path != "" {
			args = append(args, "--file="+entry.document.Path)
		}
	}
	for i, stop := range j.Stops {
		if stop.Location == nil {
			return "", fmt.Errorf("Set %s before copying the journey", journeyStopLabel(i, len(j.Stops)))
		}
		flag := "--waypoint="
		if i == 0 {
			flag = "--start="
		} else if i == len(j.Stops)-1 {
			flag = "--end="
		}
		lat := strconv.FormatFloat(stop.Location.Lat, 'f', -1, 64)
		lon := strconv.FormatFloat(stop.Location.Lon, 'f', -1, 64)
		args = append(args, flag+lat+","+lon)
	}
	for i, arg := range args {
		if i == 0 {
			continue
		}
		args[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return strings.Join(args, " "), nil
}

func (m *viewer) copyJourney() tea.Cmd {
	command, err := m.journeyCommand()
	m.dirty = true
	if err != nil {
		m.ui.notice = err.Error()
		return nil
	}
	m.ui.notice = "Journey command sent to clipboard"
	return tea.SetClipboard(command)
}
