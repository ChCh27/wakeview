package server

import (
	"net/url"
	"sort"
	"strings"

	"github.com/ChCh27/wakeview/internal/session"
)

// column is a sortable table column. Text columns sort ascending on the first click,
// numbers and dates descending.
type column struct {
	Key   string
	Label string
	Num   bool
	Desc  bool
}

// header is a column as the template draws it: a link that sorts by the column.
type header struct {
	Label string
	URL   string
	Arrow string
	Num   bool
}

// listParams are the sort and filter settings of a list page, all taken from the URL.
type listParams struct {
	Sort    string
	Desc    bool
	Query   string
	Missing bool
}

func parseList(q url.Values, cols []column, def string) listParams {
	p := listParams{Sort: def, Query: strings.TrimSpace(q.Get("q")), Missing: q.Get("missing") == "1"}
	for _, c := range cols {
		if c.Key == q.Get("sort") {
			p.Sort = c.Key
		}
	}
	p.Desc = defaultDesc(cols, p.Sort)
	switch q.Get("order") {
	case "asc":
		p.Desc = false
	case "desc":
		p.Desc = true
	}
	return p
}

func defaultDesc(cols []column, key string) bool {
	for _, c := range cols {
		if c.Key == key {
			return c.Desc
		}
	}
	return false
}

func (p listParams) values() url.Values {
	v := url.Values{}
	if p.Query != "" {
		v.Set("q", p.Query)
	}
	if p.Missing {
		v.Set("missing", "1")
	}
	return v
}

// headers builds the column links: clicking the active column flips its order.
func headers(base string, p listParams, cols []column) []header {
	var out []header
	for _, c := range cols {
		desc := c.Desc
		h := header{Label: c.Label, Num: c.Num}
		if c.Key == p.Sort {
			desc = !p.Desc
			h.Arrow = "▲"
			if p.Desc {
				h.Arrow = "▼"
			}
		}
		v := p.values()
		v.Set("sort", c.Key)
		if desc {
			v.Set("order", "desc")
		} else {
			v.Set("order", "asc")
		}
		h.URL = base + "?" + v.Encode()
		out = append(out, h)
	}
	return out
}

var projectColumns = []column{
	{Key: "name", Label: "Project"},
	{Key: "sessions", Label: "Sessions", Num: true, Desc: true},
	{Key: "memory", Label: "Memory", Num: true, Desc: true},
	{Key: "cost", Label: "Cost (API est.)", Num: true, Desc: true},
	{Key: "updated", Label: "Last active", Desc: true},
}

func filterProjects(all []session.Project, p listParams) []session.Project {
	q := strings.ToLower(p.Query)
	var out []session.Project
	for _, pr := range all {
		if p.Missing && !pr.Missing {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(pr.Name()), q) {
			continue
		}
		out = append(out, pr)
	}
	less := map[string]func(a, b session.Project) bool{
		"name":     func(a, b session.Project) bool { return strings.ToLower(a.Name()) < strings.ToLower(b.Name()) },
		"sessions": func(a, b session.Project) bool { return len(a.Sessions) < len(b.Sessions) },
		"memory":   func(a, b session.Project) bool { return a.Memories < b.Memories },
		"cost":     func(a, b session.Project) bool { return a.CostUSD < b.CostUSD },
		"updated":  func(a, b session.Project) bool { return a.Updated.Before(b.Updated) },
	}[p.Sort]
	sortBy(out, less, p.Desc)
	return out
}

var sessionColumns = []column{
	{Key: "title", Label: "Session"},
	{Key: "started", Label: "Started", Desc: true},
	{Key: "updated", Label: "Updated", Desc: true},
	{Key: "messages", Label: "Messages", Num: true, Desc: true},
	{Key: "cost", Label: "Cost (API est.)", Num: true, Desc: true},
}

func filterSessions(all []session.Summary, p listParams) []session.Summary {
	q := strings.ToLower(p.Query)
	var out []session.Summary
	for _, s := range all {
		if q != "" && !strings.Contains(strings.ToLower(s.Title), q) {
			continue
		}
		out = append(out, s)
	}
	less := map[string]func(a, b session.Summary) bool{
		"title":    func(a, b session.Summary) bool { return strings.ToLower(a.Title) < strings.ToLower(b.Title) },
		"started":  func(a, b session.Summary) bool { return a.Started.Before(b.Started) },
		"updated":  func(a, b session.Summary) bool { return a.Updated.Before(b.Updated) },
		"messages": func(a, b session.Summary) bool { return a.Messages < b.Messages },
		"cost":     func(a, b session.Summary) bool { return a.Stats.CostUSD < b.Stats.CostUSD },
	}[p.Sort]
	sortBy(out, less, p.Desc)
	return out
}

func sortBy[T any](items []T, less func(a, b T) bool, desc bool) {
	sort.SliceStable(items, func(i, j int) bool {
		if desc {
			return less(items[j], items[i])
		}
		return less(items[i], items[j])
	})
}
