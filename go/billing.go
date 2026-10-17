package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/cancelreader"
)

const billingNoticeVersion = 1
const mapboxUsageURL = "https://console.mapbox.com/account/statistics/"
const mapboxAlertsURL = "https://console.mapbox.com/account/settings/usage-thresholds/"
const mapboxPricingURL = "https://www.mapbox.com/pricing/"
const billingDocsURL = "https://github.com/adamcbrewer/mapbox-tui/blob/main/docs/MAPBOX.md#free-usage-and-billing"

const billingNotice = "Mapbox TUI aims to support personal use within Mapbox's free allowances, " +
	"but free use is not guaranteed. Online features, background requests and bugs may generate billable usage. " +
	"You are responsible for monitoring your account and any resulting charges.\n\n" +
	"Review your allowances and set up usage alerts before continuing. " +
	"Alerts do not stop usage or charges. Different products have separate allowances; some have no free allowance.\n\n" +
	"By choosing Accept risks and continue online, you acknowledge these risks.\n\n" +
	"When signing in, online features are enabled only after login succeeds.\n\n" +
	"Offline mode keeps coordinates and local files usable. " +
	"An authenticated account still checks usage once at startup, including offline."

func billingGuidance() string {
	return billingNotice + "\n\nBilling guidance: " + billingDocsURL +
		"\nAccount usage: " + mapboxUsageURL + "\nUsage alerts: " + mapboxAlertsURL +
		"\nPricing: " + mapboxPricingURL
}

func terminalHyperlink(label, url string) string {
	return ansi.SetHyperlink(url) + label + ansi.ResetHyperlink()
}

func linkedBillingGuidance() string {
	text := billingGuidance()
	for _, url := range []string{billingDocsURL, mapboxUsageURL, mapboxAlertsURL, mapboxPricingURL} {
		text = strings.ReplaceAll(text, url, terminalHyperlink(url, url))
	}
	return text
}

func billingGuidanceForOutput(output io.Writer) string {
	file, ok := output.(*os.File)
	if ok && term.IsTerminal(file.Fd()) && os.Getenv("TERM") != "dumb" {
		return linkedBillingGuidance()
	}
	return billingGuidance()
}

func (s appSettings) billingAccepted() bool {
	return s.BillingAcceptedVersion == billingNoticeVersion || os.Getenv("MAPBOX_TUI_ACCEPT_BILLING") == "1"
}

func recordBillingAcceptance() (appSettings, error) {
	return updateAppSettings(func(settings *appSettings) { settings.BillingAcceptedVersion = billingNoticeVersion })
}

// Enabling services is separate from acknowledging terms. Login callers only
// enable them after successful authentication; an explicit Go online is immediate.
func saveMapboxOffline(offline bool) (appSettings, error) {
	return updateAppSettings(func(settings *appSettings) { settings.Offline = offline })
}

func requireMapboxOnline(token string) error {
	if token == "" {
		return nil
	}
	settings, err := loadAppSettings()
	if err != nil {
		return err
	}
	if reason := settings.productAccess(token).guidance(); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	return nil
}

// Both CLI login and auth online use the same notice as the viewer confirmation.
func confirmMapboxBilling(ctx context.Context, streams authStreams, accepted bool) error {
	settings, err := loadAppSettings()
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(streams.out, billingGuidanceForOutput(streams.out)); err != nil {
		return err
	}
	if !settings.billingAccepted() && !accepted {
		input, ok := streams.in.(*os.File)
		if !ok || !term.IsTerminal(input.Fd()) {
			return fmt.Errorf("billing acknowledgement required; review the guidance, then use --accept-billing or MAPBOX_TUI_ACCEPT_BILLING=1")
		}
		fmt.Fprint(streams.out, "\nAccept risks and continue online? [y/N]: ")
		reader, err := cancelreader.NewReader(input)
		if err != nil {
			return fmt.Errorf("prepare billing acknowledgement: %w", err)
		}
		stop := context.AfterFunc(ctx, func() { reader.Cancel() })
		defer func() { stop(); reader.Close() }()
		answer, err := bufio.NewReader(reader).ReadString('\n')
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return fmt.Errorf("read billing acknowledgement: %w", err)
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			if _, err := saveMapboxOffline(true); err != nil {
				return err
			}
			return fmt.Errorf("continuing offline; coordinates and imported files remain available")
		}
	}
	_, err = recordBillingAcceptance()
	return err
}
