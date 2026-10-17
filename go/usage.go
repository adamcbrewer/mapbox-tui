package main

import (
	"context"
	"math"
	"time"
)

type productUsage struct {
	Name         string `json:"name"`
	Unit         string `json:"unit"`
	Total        int64  `json:"total"`
	ReportedDays int    `json:"reported_days"`
	LatestDay    string `json:"latest_reported_day,omitempty"`
}

// Keep billing products distinct: sessions and requests are different units,
// and traffic tiles share Vector Tiles usage with the basemap.
var appUsageProducts = []struct{ name, unit string }{
	{name: "Vector Tiles API", unit: "requests"},
	{name: "Search Box API - Sessions", unit: "sessions"},
	{name: "Directions API", unit: "requests"},
	{name: "Geocoding API", unit: "requests"},
	{name: "Temporary Geocoding API", unit: "requests"},
	{name: "Permanent Geocoding API", unit: "requests"},
}

func currentUsagePeriod(now time.Time) usagePeriod {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return usagePeriod{Start: start.Format(time.DateOnly), End: now.Format(time.DateOnly)}
}

func (r usageReport) stale(now time.Time) bool {
	return r.GeneratedAt != nil && (now.Sub(*r.GeneratedAt) > 24*time.Hour ||
		(r.Period != nil && r.Period.Start != currentUsagePeriod(now).Start))
}

func (c *mapboxCLI) usage(ctx context.Context, account, tokenID string, period usagePeriod) usageReport {
	report := usageReport{State: "unavailable", Account: account, TokenID: tokenID, Products: []productUsage{}}
	args := []string{"usage", "--period-start", period.Start, "--period-end", period.End}
	if tokenID != "" {
		args = append(args, "--token-id", tokenID)
	}
	var result struct {
		GeneratedAt time.Time `json:"generated_at"`
		Data        *struct {
			Period          usagePeriod `json:"period"`
			TokenID         string      `json:"token_id"`
			NextPeriodStart *string     `json:"next_period_start"`
			Products        map[string]struct {
				Daily []struct {
					Date  string `json:"date"`
					Usage *int64 `json:"usage"`
				} `json:"daily"`
			} `json:"products"`
		} `json:"data"`
	}
	if err := c.query(ctx, args, nil, &result); err != nil {
		report.Guidance = err.Error() + "; statistics access is separate from map access"
		return report
	}
	if result.Data == nil || result.Data.Period != period {
		report.Guidance = "Mapbox usage response is missing the requested UTC period"
		return report
	}
	if result.Data.TokenID != tokenID {
		report.Guidance = "Mapbox usage response does not match the requested account/token scope"
		return report
	}
	if result.GeneratedAt.IsZero() || result.GeneratedAt.After(time.Now().Add(5*time.Minute)) {
		report.Guidance = "Mapbox usage response has an invalid generation timestamp"
		return report
	}
	if result.Data.Products == nil || result.Data.NextPeriodStart != nil {
		report.Guidance = "Mapbox usage response is incomplete"
		return report
	}
	products := []productUsage{}
	vectorDaily := map[string]int64{}
	var vectorTotal int64
	for _, definition := range appUsageProducts {
		raw, ok := result.Data.Products[definition.name]
		if !ok {
			continue
		}
		product := productUsage{Name: definition.name, Unit: definition.unit}
		dates := map[string]bool{}
		for _, day := range raw.Daily {
			date, err := time.Parse(time.DateOnly, day.Date)
			validDate := err == nil && date.Format(time.DateOnly) >= period.Start && day.Date <= period.End
			if !validDate || dates[day.Date] || day.Usage == nil || *day.Usage < 0 {
				report.Guidance = "Mapbox usage response has invalid daily figures"
				return report
			}
			if *day.Usage > math.MaxInt64-product.Total {
				report.Guidance = "Mapbox usage response has invalid totals"
				return report
			}
			dates[day.Date] = true
			product.Total += *day.Usage
			product.LatestDay = max(product.LatestDay, day.Date)
			if definition.name == "Vector Tiles API" {
				vectorDaily[day.Date] = *day.Usage
				vectorTotal += *day.Usage
			}
		}
		product.ReportedDays = len(dates)
		// An empty product has no reported count, rather than a confirmed zero.
		if product.ReportedDays > 0 {
			products = append(products, product)
		}
	}
	now := time.Now().UTC()
	report.State, report.Period = "empty", &result.Data.Period
	report.GeneratedAt, report.FetchedAt = &result.GeneratedAt, &now
	report.Products, report.Daily, report.Requests = products, vectorDaily, vectorTotal
	if len(products) > 0 {
		report.State = "available"
	}
	return report
}

// Startup statistics must never call token validation or billable service probes.
func collectAccountUsage(ctx context.Context) usageReport {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	report := usageReport{State: "unavailable", Products: []productUsage{}}
	cli, status := checkMapboxCLI(ctx)
	if cli == nil {
		report.Guidance = status.Guidance
		return report
	}
	var identity mapboxIdentity
	if err := cli.query(ctx, []string{"auth", "whoami"}, nil, &identity); err != nil {
		report.Guidance = err.Error() + "; account usage requires a saved Mapbox login with statistics access"
		return report
	}
	account := safeMapboxID(identity.Account)
	if account == "" {
		report.Guidance = "Mapbox did not identify the authenticated account"
		return report
	}
	return cli.usage(ctx, account, "", currentUsagePeriod(time.Now()))
}
