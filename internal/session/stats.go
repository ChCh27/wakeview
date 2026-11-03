package session

import (
	"sort"
	"time"
)

// costState holds the fields of a cost-state record: running totals of one Claude Code process.
// A resumed session starts a new process, whose totals start from zero again.
type costState struct {
	StartTime         int64                 `json:"startTime"`
	TotalCostUSD      float64               `json:"totalCostUSD"`
	TotalDuration     int64                 `json:"totalDuration"`
	TotalAPIDuration  int64                 `json:"totalAPIDuration"`
	TotalLinesAdded   int                   `json:"totalLinesAdded"`
	TotalLinesRemoved int                   `json:"totalLinesRemoved"`
	ModelUsage        map[string]modelUsage `json:"modelUsage"`
}

type modelUsage struct {
	InputTokens              int64   `json:"inputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
	CostUSD                  float64 `json:"costUSD"`
}

// Stats are the totals of a session, as estimated by Claude Code at API prices.
type Stats struct {
	// Known is false when the session has no cost-state records.
	Known        bool
	CostUSD      float64
	Duration     time.Duration
	APIDuration  time.Duration
	LinesAdded   int
	LinesRemoved int
	Models       []ModelStats
}

type ModelStats struct {
	Model      string
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	CostUSD    float64
}

// statsFrom adds up the last cost-state of every process, keyed by its start time.
func statsFrom(last map[int64]costState) Stats {
	var st Stats
	models := map[string]*ModelStats{}
	for _, c := range last {
		st.Known = true
		st.CostUSD += c.TotalCostUSD
		st.Duration += time.Duration(c.TotalDuration) * time.Millisecond
		st.APIDuration += time.Duration(c.TotalAPIDuration) * time.Millisecond
		st.LinesAdded += c.TotalLinesAdded
		st.LinesRemoved += c.TotalLinesRemoved
		for name, u := range c.ModelUsage {
			m := models[name]
			if m == nil {
				m = &ModelStats{Model: name}
				models[name] = m
			}
			m.Input += u.InputTokens
			m.Output += u.OutputTokens
			m.CacheRead += u.CacheReadInputTokens
			m.CacheWrite += u.CacheCreationInputTokens
			m.CostUSD += u.CostUSD
		}
	}
	for _, m := range models {
		st.Models = append(st.Models, *m)
	}
	sort.Slice(st.Models, func(i, j int) bool { return st.Models[i].CostUSD > st.Models[j].CostUSD })
	return st
}
