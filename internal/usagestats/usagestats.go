// Package usagestats aggregates locally stored usage events into the report
// behind `tokitoki stats`. It reads nothing and talks to no server: front-ends
// render these numbers straight from the local database, which is exactly why
// they work before an API key is ever configured.
package usagestats

import (
	"math"
	"sort"
	"time"

	"github.com/tokitoki-dev/tokitoki-cli/internal/usage"
)

// The idle rule. It is the server's rule (tracklm-nextjs/lib/active-time.ts),
// copied by value: events are walked in time order and the gap to the previous
// one is counted as active when it is within idleTimeout; a longer gap means
// "stepped away" and counts nothing. Every partition that had any event at all
// gets dayFloor once per day, so a lone event still registers some time.
//
// The same numbers as the dashboard, so the editor panel and the web page
// never disagree about how long the same day was.
const (
	idleTimeout = 15 * time.Minute
	dayFloor    = time.Minute
)

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
	// Project carries the same report narrowed to the one project the caller
	// asked for (stats --project), built from the same database read. One
	// invocation answers both "overall" and "this project" — front-ends never
	// need two processes for one panel.
	Project *Report `json:"project,omitempty"`
}

// BuildForProject aggregates the full window plus a nested sub-report for one
// project. The sub-report exists even when the project has no events: a
// zero-filled report is an answer ("nothing recorded"), a missing one is a
// question the caller has to special-case.
func BuildForProject(entries []usage.Entry, days int, now time.Time, project string) Report {
	report := Build(entries, days, now)
	scoped := make([]usage.Entry, 0)
	for _, entry := range entries {
		if entry.Project == project {
			scoped = append(scoped, entry)
		}
	}
	sub := Build(scoped, days, now)
	report.Project = &sub
	return report
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
	Name          string `json:"name"`
	Events        int    `json:"events"`
	TotalTokens   uint64 `json:"total_tokens"`
	ActiveSeconds int64  `json:"active_seconds"`
}

// clock accumulates active time for one partition — one calendar day, or one
// group on one day — under the idle rule.
type clock struct {
	last   time.Time
	active time.Duration
}

func (c *clock) tick(ts time.Time) {
	if !c.last.IsZero() {
		if gap := ts.Sub(c.last); gap <= idleTimeout {
			c.active += gap
		}
	}
	c.last = ts
}

// seconds is the partition's credited time: what the gaps added up to plus the
// floor, rounded once at the end like the server's SUM then round().
func (c *clock) seconds() int64 {
	return int64(math.Round((c.active + dayFloor).Seconds()))
}

// groupAgg carries a group's per-day clocks while aggregating; they collapse
// into ActiveSeconds once counting is done.
type groupAgg struct {
	GroupStat
	days map[int]*clock
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

	// The database read is in no particular order and the gap rule needs time
	// order. Sorted on a copy: the caller's slice is not ours to reorder.
	ordered := make([]usage.Entry, len(entries))
	copy(ordered, entries)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Timestamp.Before(ordered[j].Timestamp)
	})

	providers := make(map[string]*groupAgg)
	models := make(map[string]*groupAgg)
	projects := make(map[string]*groupAgg)
	dayClocks := make(map[int]*clock)

	for _, entry := range ordered {
		local := entry.Timestamp.Local()
		index, ok := dayIndex[local.Format(time.DateOnly)]
		if !ok {
			continue
		}

		// A file edit is not a request: it is a side effect of one that is
		// already counted. Its lines still matter and its activity still
		// moves the clock; only the request count leaves it out.
		isRequest := entry.EventKind != usage.EventKindFileEdit

		day := &report.Daily[index]
		if isRequest {
			day.Events++
			report.Totals.Events++
		}
		day.TotalTokens += entry.Usage.TotalTokens
		report.Totals.TotalTokens += entry.Usage.TotalTokens
		report.Totals.InputTokens += entry.Usage.InputTokens
		report.Totals.OutputTokens += entry.Usage.OutputTokens

		tickDay(dayClocks, index, entry.Timestamp)

		accumulate(providers, string(entry.Provider), entry, index, isRequest)
		accumulate(models, entry.Model, entry, index, isRequest)
		accumulate(projects, entry.Project, entry, index, isRequest)
	}

	for index, c := range dayClocks {
		report.Daily[index].ActiveSeconds = c.seconds()
		report.Totals.ActiveSeconds += c.seconds()
	}
	report.Providers = sorted(providers)
	report.Models = sorted(models)
	report.Projects = sorted(projects)
	return report
}

func tickDay(days map[int]*clock, index int, ts time.Time) {
	c := days[index]
	if c == nil {
		c = &clock{}
		days[index] = c
	}
	c.tick(ts)
}

func accumulate(groups map[string]*groupAgg, name string, entry usage.Entry, index int, isRequest bool) {
	if name == "" {
		return
	}
	group := groups[name]
	if group == nil {
		group = &groupAgg{GroupStat: GroupStat{Name: name}, days: make(map[int]*clock)}
		groups[name] = group
	}
	if isRequest {
		group.Events++
	}
	group.TotalTokens += entry.Usage.TotalTokens
	tickDay(group.days, index, entry.Timestamp)
}

func sorted(groups map[string]*groupAgg) []GroupStat {
	result := make([]GroupStat, 0, len(groups))
	for _, group := range groups {
		for _, c := range group.days {
			group.ActiveSeconds += c.seconds()
		}
		result = append(result, group.GroupStat)
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
