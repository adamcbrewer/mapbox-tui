package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func usageFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/mapbox-usage.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestUsageFiltersProductsAndKeepsUnitsAndReportedDays(t *testing.T) {
	fakeMapbox(t, "good")
	t.Setenv("MAPBOX_TUI_TEST_USAGE_RESPONSE", usageFixture(t))
	cli, err := findMapboxCLI(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	period := usagePeriod{Start: "2026-10-01", End: "2026-10-17"}
	r := cli.usage(t.Context(), "sampleaccount", "", period)
	if r.State != "available" || len(r.Products) != 5 || r.Requests != 123 {
		t.Fatalf("unexpected usage: %+v", r)
	}
	if product := r.Products[0]; product.ReportedDays != 2 || product.LatestDay != "2026-10-02" {
		t.Fatalf("days were confused with period length: %+v", product)
	}
	if product := r.Products[1]; product.Total != 8 || product.Unit != "sessions" {
		t.Fatalf("search sessions were conflated with requests: %+v", product)
	}
	if product := r.Products[3]; product.Total != 0 || product.ReportedDays != 1 {
		t.Fatalf("explicitly reported zero was lost: %+v", product)
	}
	encoded, err := json.Marshal(r)
	if err != nil || strings.Contains(string(encoded), "Hosting") || strings.Contains(string(encoded), "Web") {
		t.Fatal("unrelated products leaked into app usage", err)
	}
}

func TestUsageRejectsMalformedOrIncompleteReports(t *testing.T) {
	for _, test := range []struct{ name, old, replacement string }{
		{name: "negative", old: `"usage": 100`, replacement: `"usage": -1`},
		{name: "missing count", old: `"usage": 100`, replacement: `"other": 100`},
		{name: "null count", old: `"usage": 100`, replacement: `"usage": null`},
		{name: "overflow", old: `"usage": 100`, replacement: `"usage": 9223372036854775807`},
		{name: "duplicate day", old: `"date": "2026-10-02", "usage": 23`, replacement: `"date": "2026-10-01", "usage": 23`},
		{name: "outside period", old: `"date": "2026-10-02", "usage": 23`, replacement: `"date": "2026-09-30", "usage": 23`},
		{name: "token mismatch", old: `"token_id": null`, replacement: `"token_id": "another-token"`},
		{name: "partial page", old: `"next_period_start": null`, replacement: `"next_period_start": "2026-10-10"`},
		{name: "missing timestamp", old: `"generated_at": "2026-10-17T08:00:00Z"`, replacement: `"generated_at": null`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakeMapbox(t, "good")
			t.Setenv("MAPBOX_TUI_TEST_USAGE_RESPONSE", strings.Replace(usageFixture(t), test.old, test.replacement, 1))
			cli, err := findMapboxCLI(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			r := cli.usage(t.Context(), "sampleaccount", "", usagePeriod{Start: "2026-10-01", End: "2026-10-17"})
			if r.State != "unavailable" || len(r.Products) != 0 || r.Guidance == "" {
				t.Fatalf("malformed report looked usable: %+v", r)
			}
		})
	}
}

func TestAccountUsageWorksOfflineWithoutProductProbes(t *testing.T) {
	for _, scenario := range []string{"good", "usage-forbidden", "loggedout", "no-usage"} {
		t.Run(scenario, func(t *testing.T) {
			fakeMapbox(t, scenario)
			t.Setenv("MAPBOX_TUI_ACCEPT_BILLING", "")
			t.Setenv("MAPBOX_ACCESS_TOKEN", "pk.foreign-account")
			if err := saveAppSettings(appSettings{Offline: true}); err != nil {
				t.Fatal(err)
			}
			r := collectAccountUsage(t.Context())
			if scenario == "good" && (r.State != "available" || r.Account != "sampleaccount") {
				t.Fatalf("offline account statistics unavailable: %+v", r)
			}
			if scenario == "usage-forbidden" || scenario == "loggedout" {
				if r.State != "unavailable" || len(r.Products) != 0 {
					t.Fatal("unavailable usage was reported as zero")
				}
			}
			calls, err := os.ReadFile(os.Getenv("MAPBOX_TUI_TEST_LOG"))
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
				var call struct{ Args []string }
				if err := json.Unmarshal([]byte(line), &call); err != nil {
					t.Fatal(err)
				}
				args := strings.Join(call.Args, " ")
				if !strings.Contains(args, " auth whoami") && !strings.Contains(args, " usage ") {
					t.Fatal("usage requested a token or product probe", args)
				}
			}
			if scenario == "good" && *r.Period != currentUsagePeriod(time.Now()) {
				t.Fatal("startup usage did not use the current UTC month", r.Period)
			}
		})
	}
}

func TestUsageUTCMonthAndStaleness(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	local := now.In(time.FixedZone("west", -7*3600))
	if got := currentUsagePeriod(local); got.Start != "2026-11-01" || got.End != "2026-11-01" {
		t.Fatal("billing period followed local date instead of UTC", got)
	}
	generated := now.Add(-time.Hour)
	report := usageReport{GeneratedAt: &generated, Period: &usagePeriod{Start: "2026-10-01", End: "2026-10-31"}}
	if !report.stale(now) {
		t.Fatal("previous-month report was presented as current")
	}
	period := currentUsagePeriod(now)
	report.Period = &period
	if report.stale(now) {
		t.Fatal("recent current-month report marked stale")
	}
	generated = now.Add(-25 * time.Hour)
	if !report.stale(now) {
		t.Fatal("old report not marked stale")
	}
}
