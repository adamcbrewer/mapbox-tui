package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type accountUsageResult struct {
	id     uint64
	report usageReport
}

type mapboxLinkResult struct {
	url string
	err error
}

func openMapboxLink(url string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "xdg-open", url)
		switch runtime.GOOS {
		case "darwin":
			command = exec.CommandContext(ctx, "open", url)
		case "windows":
			command = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
		}
		err := command.Run()
		return mapboxLinkResult{url: url, err: err}
	}
}

func (m *viewer) cancelAccountUsage() {
	if m.usageCancel != nil {
		m.usageCancel()
		m.usageCancel = nil
	}
	m.usageID++
}

func (m *viewer) fetchAccountUsage() tea.Cmd {
	m.cancelAccountUsage()
	ctx, cancel := context.WithCancel(m.ctx)
	m.usageCancel = cancel
	m.usage = usageReport{State: "loading", Products: []productUsage{}}
	id := m.usageID
	telemetry := m.mapboxTelemetryOverride()
	return func() tea.Msg {
		return accountUsageResult{id: id, report: collectAccountUsageWithTelemetry(ctx, telemetry)}
	}
}

type billingIntent uint8

const (
	billingNoIntent billingIntent = iota
	billingShowGuidance
	billingEnableOnline
	billingSignIn
	billingTokenSignIn
)

func (m *viewer) openBilling(intent billingIntent) tea.Cmd {
	m.billingIntent = intent
	if m.ui.dialog == dialogMapboxBilling {
		return nil
	}
	return m.pushDialog(dialogMapboxBilling)
}

func (m *viewer) setMapboxOffline(offline bool) tea.Cmd {
	preferences, err := saveMapboxOffline(offline)
	if err != nil && !offline {
		m.ui.notice = "Could not save Mapbox setting: " + err.Error()
		m.dirty = true
		return nil
	}
	m.preferences.Offline = offline
	if err == nil {
		m.preferences.BillingAcceptedVersion = preferences.BillingAcceptedVersion
	}
	m.cancelAuth()
	m.cancelPlaceSearch()
	m.workspace.cancelJourneyRoutes()
	if offline {
		if m.ui.dialog != dialogMapbox {
			m.closeDialog()
		}
		cmd := m.acceptCredentials(credentialResult{id: m.authID, credential: m.credential})
		m.authMessage = "Offline mode saved"
		if err != nil {
			m.authMessage = "Offline for this session; could not save preference: " + err.Error()
			m.ui.notice = m.authMessage
			if m.ui.dialog != dialogMapbox {
				m.openDialog(dialogMapbox)
			}
		}
		return cmd
	}
	return m.discoverCredentials(false, false)
}

func (m *viewer) acceptBilling() tea.Cmd {
	preferences, err := recordBillingAcceptance()
	if err != nil {
		m.ui.notice = "Could not save billing acknowledgement: " + err.Error()
		m.dirty = true
		return nil
	}
	m.preferences.BillingAcceptedVersion = preferences.BillingAcceptedVersion
	intent := m.billingIntent
	m.billingIntent = billingNoIntent
	m.backDialog()
	if intent == billingSignIn {
		return m.beginMapboxLogin()
	}
	if intent == billingTokenSignIn {
		return m.beginMapboxTokenLogin()
	}
	return m.setMapboxOffline(false)
}

var billingActions = []viewerCommand{
	{title: "Continue offline", action: "offline"},
	{title: "Accept risks and continue online", action: "online"},
}

func (m *viewer) updateBillingDialog(msg tea.Msg) tea.Cmd {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			if m.billingIntent == billingShowGuidance {
				m.billingIntent = billingNoIntent
				return m.backDialog()
			}
			m.billingIntent = billingNoIntent
			return m.setMapboxOffline(true)
		case "tab", "right":
			m.ui.selected = menuStep(m.ui.selected, 1, len(billingActions))
			return nil
		case "shift+tab", "left":
			m.ui.selected = menuStep(m.ui.selected, -1, len(billingActions))
			return nil
		case "enter":
			switch billingActions[m.ui.selected].action {
			case "online":
				return m.acceptBilling()
			case "offline":
				m.billingIntent = billingNoIntent
				return m.setMapboxOffline(true)
			}
		}
	}
	var cmd tea.Cmd
	m.ui.page, cmd = m.ui.page.Update(msg)
	return cmd
}

func (m *viewer) billingDialogContent(width, height int) (string, string, string) {
	choice := "> " + billingActions[m.ui.selected].title
	choice = ansi.Truncate(choice, width, "…")
	actions := []string{m.styles.selected.Render(pad(choice, width))}
	if width >= 56 && height >= 5 {
		labels := make([]string, len(billingActions))
		for i, action := range billingActions {
			label := action.title
			labels[i] = "  " + label
			if i == m.ui.selected {
				labels[i] = m.styles.selected.Render("> " + label)
			}
		}
		actions = []string{strings.Join(labels, "  ")}
	}
	m.ui.page.SetWidth(width)
	m.ui.page.SetHeight(max(1, height-len(actions)-1))
	m.ui.page.SetContent(ansi.Wordwrap(linkedBillingGuidance(), width, ""))
	rows := []string{}
	if height > 1 {
		rows = append(rows, m.ui.page.View())
	}
	if height > 2 {
		rows = append(rows, "")
	}
	rows = append(rows, actions...)
	hints := "↑/↓ read   tab choice   enter choose   esc offline"
	if m.billingIntent == billingShowGuidance {
		hints = "↑/↓ read   tab choice   enter choose   esc back"
	}
	return "Mapbox billing risks", strings.Join(rows, "\n"), hints
}

func (m *viewer) usageSummary() []string {
	r := m.usage
	switch r.State {
	case "loading":
		return []string{"Account usage: checking…"}
	case "available", "empty":
		rows := m.usageAccountSummary()
		period := r.Period.Start + " to " + r.Period.End + " UTC"
		if r.stale(time.Now()) {
			period = "Stale · " + period
		}
		rows = append(rows, period)
		for _, product := range r.Products {
			rows = append(rows, fmt.Sprintf("%s: %d %s", product.Name, product.Total, product.Unit))
		}
		if len(r.Products) == 0 {
			rows = append(rows, "No app product usage reported")
		}
		return rows
	default:
		return []string{"Account usage unavailable · view on Mapbox"}
	}
}

func (m *viewer) usageAccountSummary() []string {
	account := "Account usage"
	if m.usage.Account != "" {
		account = "Usage: " + m.usage.Account + " (saved login)"
	}
	rows := []string{account}
	if m.credential.source == "environment" && m.credential.token != "" {
		rows = append(rows, "Map token: environment; account may differ")
	}
	if m.credential.source == "saved_token" && m.credential.token != "" {
		rows = append(rows, "Map token: saved public token; account may differ")
	}
	return rows
}

func (m *viewer) usageDialogContent(width, height int) (string, string, string) {
	var content strings.Builder
	r := m.usage
	if r.Period != nil {
		fmt.Fprintln(&content, strings.Join(m.usageAccountSummary(), "\n"))
		fmt.Fprintf(&content, "%s to %s UTC\n", r.Period.Start, r.Period.End)
		if r.stale(time.Now()) {
			content.WriteString("Stale report; restart to fetch current usage.\n")
		}
	} else {
		fmt.Fprintln(&content, m.usageSummary()[0])
	}
	for _, product := range r.Products {
		fmt.Fprintf(&content, "\n%s\n  %d %s · days reported: %d\n  Latest reported: %s\n",
			product.Name, product.Total, product.Unit, product.ReportedDays, product.LatestDay)
	}
	if r.State == "empty" {
		content.WriteString("\nNo app product usage reported.\n")
	}
	if r.GeneratedAt != nil {
		fmt.Fprintf(&content, "\nReport generated: %s\n", r.GeneratedAt.UTC().Format(time.RFC3339))
	}
	if r.FetchedAt != nil {
		fmt.Fprintf(&content, "Fetched: %s\n", r.FetchedAt.UTC().Format(time.RFC3339))
	}
	if r.Account != "" && r.Period == nil {
		fmt.Fprintf(&content, "Account: %s (saved login)\n", r.Account)
	}
	if r.Guidance != "" {
		fmt.Fprintf(&content, "\n%s\n", r.Guidance)
	}
	content.WriteString("\nAccount-wide figures can include other apps. Statistics may lag 24 hours. " +
		"Days reported does not establish completeness. Missing products are not confirmed zero usage. " +
		"These counts are not an invoice or remaining allowance.\n\n")
	content.WriteString(terminalHyperlink("View account usage on Mapbox (Enter)", mapboxUsageURL) + ":\n" +
		terminalHyperlink(mapboxUsageURL, mapboxUsageURL))
	m.ui.page.SetWidth(width)
	m.ui.page.SetHeight(max(1, height))
	m.ui.page.SetContent(ansi.Wordwrap(content.String(), width, ""))
	return "Mapbox usage", m.ui.page.View(), "↑/↓ scroll   enter web usage   esc back"
}
