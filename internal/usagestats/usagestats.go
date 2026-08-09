// Package usagestats aggregates locally stored usage events into the report
// behind `tokitoki stats`. It reads nothing and talks to no server: front-ends
// render these numbers straight from the local database, which is exactly why
// they work before an API key is ever configured.
package usagestats

import (
	"sort"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// activeBucket is the granularity of the activity estimate. Any event inside
// a bucket marks the whole bucket active; editor heartbeats are already
// throttled to one per entity per two minutes, so a finer bucket would only
// count throttling artifacts, not more activity.
const activeBucket = 2 * time.Minute

type Report struct {
	Days   int    `json:"days"`
	From   string `json:"from"`
	To     string `json:"to"`
	Totals Totals `json:"totals"`
	// Daily is dense: one entry per day of the window, zero-filled, oldest
	// first. Charts consume it index-for-index with no gap handling.
	Daily     []DailyStat `json:"daily"`
	Providers []GroupStat `json:"providers"`
	Models    []GroupStat `json:"models"`
	Projects  []GroupStat `json:"projects"`
}

type Totals struct {
	Events        int    `json:"events"`
	TotalTokens   uint64 `json:"total_tokens"`
	InputTokens   uint64 `json:"input_tokens"`
	OutputTokens  uint64 `json:"output_tokens"`
	ActiveSeconds int64  `json:"active_seconds"`
}

type DailyStat struct {
	Date          string `json:"date"`
	Events        int    `json:"events"`
	TotalTokens   uint64 `json:"total_tokens"`
	ActiveSeconds int64  `json:"active_seconds"`
}

type GroupStat struct {
	Name        string `json:"name"`
	Events      int    `json:"events"`
	TotalTokens uint64 `json:"total_tokens"`
}

// Build aggregates entries into the report for the window of `days` calendar
// days ending on now's local date. Entries outside the window are ignored, so
// callers may pass a superset (the database read is by timestamp, the window
// boundary is a local-date fact only this function knows).
func Build(entries []usage.Entry, days int, now time.Time) Report {
	if days < 1 {
		days = 1
	}
	now = now.Local()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	from := today.AddDate(0, 0, -(days - 1))

	report := Report{
		Days:  days,
		From:  from.Format(time.DateOnly),
		To:    today.Format(time.DateOnly),
		Daily: make([]DailyStat, days),
	}
	dayIndex := make(map[string]int, days)
	for i := range report.Daily {
		date := from.AddDate(0, 0, i).Format(time.DateOnly)
		report.Daily[i] = DailyStat{Date: date}
		dayIndex[date] = i
	}

	providers := make(map[string]*GroupStat)
	models := make(map[string]*GroupStat)
	projects := make(map[string]*GroupStat)
	activeBuckets := make(map[int64]struct{})
	dailyBuckets := make([]map[int64]struct{}, days)

	for _, entry := range entries {
		local := entry.Timestamp.Local()
		index, ok := dayIndex[local.Format(time.DateOnly)]
		if !ok {
			continue
		}

		day := &report.Daily[index]
		day.Events++
		day.TotalTokens += entry.Usage.TotalTokens
		report.Totals.Events++
		report.Totals.TotalTokens += entry.Usage.TotalTokens
		report.Totals.InputTokens += entry.Usage.InputTokens
		report.Totals.OutputTokens += entry.Usage.OutputTokens

		bucket := local.Unix() / int64(activeBucket/time.Second)
		activeBuckets[bucket] = struct{}{}
		if dailyBuckets[index] == nil {
			dailyBuckets[index] = make(map[int64]struct{})
		}
		dailyBuckets[index][bucket] = struct{}{}

		accumulate(providers, string(entry.Provider), entry)
		accumulate(models, entry.Model, entry)
		accumulate(projects, entry.Project, entry)
	}

	report.Totals.ActiveSeconds = int64(len(activeBuckets)) * int64(activeBucket/time.Second)
	for i, buckets := range dailyBuckets {
		report.Daily[i].ActiveSeconds = int64(len(buckets)) * int64(activeBucket/time.Second)
	}
	report.Providers = sorted(providers)
	report.Models = sorted(models)
	report.Projects = sorted(projects)
	return report
}

func accumulate(groups map[string]*GroupStat, name string, entry usage.Entry) {
	if name == "" {
		return
	}
	group := groups[name]
	if group == nil {
		group = &GroupStat{Name: name}
		groups[name] = group
	}
	group.Events++
	group.TotalTokens += entry.Usage.TotalTokens
}

func sorted(groups map[string]*GroupStat) []GroupStat {
	result := make([]GroupStat, 0, len(groups))
	for _, group := range groups {
		result = append(result, *group)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TotalTokens != result[j].TotalTokens {
			return result[i].TotalTokens > result[j].TotalTokens
		}
		if result[i].Events != result[j].Events {
			return result[i].Events > result[j].Events
		}
		return result[i].Name < result[j].Name
	})
	return result
}
