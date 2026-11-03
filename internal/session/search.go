package session

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

type SearchOptions struct {
	Query string
	// Tools also searches tool call inputs and results.
	Tools bool
	// Project limits the search to one project directory; empty searches all.
	Project string
}

// Snippet is a piece of text around the first match.
type Snippet struct {
	Before, Match, After string
}

type Hit struct {
	Anchor  string
	Kind    Kind
	Snippet Snippet
}

// SessionHits are the matches in one session. Hits holds the first few; Count is the total.
type SessionHits struct {
	Project Project
	Summary Summary
	Count   int
	Hits    []Hit
}

type MemoryHit struct {
	Project Project
	Memory  Memory
	Snippet Snippet
}

const hitsPerSession = 3

// Terms splits a query into lowercase words; every word must occur in the same message.
func Terms(query string) []string {
	return strings.Fields(strings.ToLower(query))
}

func (s *Store) Search(opt SearchOptions) ([]SessionHits, []MemoryHit, error) {
	terms := Terms(opt.Query)
	if len(terms) == 0 {
		return nil, nil, nil
	}
	projects, err := s.Projects()
	if err != nil {
		return nil, nil, err
	}
	var sessions []SessionHits
	var memories []MemoryHit
	for _, p := range projects {
		if opt.Project != "" && p.Dir != opt.Project {
			continue
		}
		mems, err := s.Memories(p.Dir)
		if err != nil {
			return nil, nil, err
		}
		for _, m := range mems {
			if sn, ok := matchMemory(m, terms); ok {
				memories = append(memories, MemoryHit{Project: p, Memory: m, Snippet: sn})
			}
		}
		for _, sum := range p.Sessions {
			sh, err := s.searchSession(p, sum, terms, opt.Tools)
			if err != nil {
				return nil, nil, err
			}
			if sh.Count > 0 {
				sessions = append(sessions, sh)
			}
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Summary.Updated.After(sessions[j].Summary.Updated) })
	sort.Slice(memories, func(i, j int) bool { return memories[i].Memory.Modified.After(memories[j].Memory.Modified) })
	return sessions, memories, nil
}

func (s *Store) searchSession(p Project, sum Summary, terms []string, tools bool) (SessionHits, error) {
	sh := SessionHits{Project: p, Summary: sum}
	entries, err := s.searchEntries(filepath.Join(s.root, p.Dir, sum.ID+".jsonl"))
	if err != nil {
		return sh, err
	}
	for _, e := range entries {
		if e.Kind == KindTool && !tools {
			continue
		}
		if !containsAll(e.Lower, terms) {
			continue
		}
		sh.Count++
		if len(sh.Hits) < hitsPerSession {
			sn, _ := snippet(e.Text, terms[0])
			sh.Hits = append(sh.Hits, Hit{Anchor: e.Anchor, Kind: e.Kind, Snippet: sn})
		}
	}
	return sh, nil
}

// searchEntry is the searchable text of one entry, kept in memory between searches.
type searchEntry struct {
	Anchor string
	Kind   Kind
	Text   string
	Lower  string
}

type cachedSearch struct {
	mod     time.Time
	size    int64
	entries []searchEntry
}

// searchEntries returns the searchable text of a session, decoding the file only when it changed.
func (s *Store) searchEntries(path string) ([]searchEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	c, ok := s.search[path]
	s.mu.Unlock()
	if ok && c.mod.Equal(info.ModTime()) && c.size == info.Size() {
		return c.entries, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	batch, err := (&Decoder{}).decode(f, false)
	if err != nil {
		return nil, err
	}
	var entries []searchEntry
	for _, e := range batch.Entries {
		text := searchText(e)
		if text == "" {
			continue
		}
		entries = append(entries, searchEntry{Anchor: e.Anchor, Kind: e.Kind, Text: text, Lower: strings.ToLower(text)})
	}
	s.mu.Lock()
	s.search[path] = cachedSearch{mod: info.ModTime(), size: info.Size(), entries: entries}
	s.mu.Unlock()
	return entries, nil
}

func containsAll(lower string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(lower, t) {
			return false
		}
	}
	return true
}

func searchText(e Entry) string {
	switch e.Kind {
	case KindPrompt, KindText:
		return e.Text
	case KindTool:
		parts := []string{e.Tool.Name}
		for _, f := range e.Tool.Input {
			parts = append(parts, f.Value)
		}
		if e.Tool.Result != nil {
			parts = append(parts, e.Tool.Result.Text)
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func matchMemory(m Memory, terms []string) (Snippet, bool) {
	text := strings.Join([]string{m.Name, m.Description, m.Body}, "\n")
	if _, ok := match(text, terms); !ok {
		return Snippet{}, false
	}
	// Prefer a snippet from the body or description over the bare name.
	for _, part := range []string{m.Body, m.Description} {
		for _, t := range terms {
			if sn, ok := snippet(part, t); ok {
				return sn, true
			}
		}
	}
	return Snippet{Match: m.Title()}, true
}

// match reports whether text contains every term, with a snippet around the first one.
func match(text string, terms []string) (Snippet, bool) {
	if !containsAll(strings.ToLower(text), terms) {
		return Snippet{}, false
	}
	sn, _ := snippet(text, terms[0])
	return sn, true
}

const (
	snippetBefore = 60
	snippetAfter  = 140
)

// snippet cuts the text around the first case-insensitive occurrence of term.
// It works on runes lowered one by one, so positions in the lowered copy match the original.
func snippet(text, term string) (Snippet, bool) {
	rs := []rune(text)
	lower := make([]rune, len(rs))
	for i, r := range rs {
		lower[i] = unicode.ToLower(r)
	}
	tr := []rune(term)
	at := -1
	for i := 0; i+len(tr) <= len(lower); i++ {
		if runesEqual(lower[i:i+len(tr)], tr) {
			at = i
			break
		}
	}
	if at < 0 {
		return Snippet{}, false
	}
	start := max(0, at-snippetBefore)
	end := min(len(rs), at+len(tr)+snippetAfter)
	sn := Snippet{
		Before: squash(string(rs[start:at])),
		Match:  string(rs[at : at+len(tr)]),
		After:  squash(string(rs[at+len(tr) : end])),
	}
	if start > 0 {
		sn.Before = "…" + strings.TrimLeft(sn.Before, " ")
	}
	if end < len(rs) {
		sn.After = strings.TrimRight(sn.After, " ") + "…"
	}
	return sn, true
}

func runesEqual(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// squash collapses runs of whitespace, newlines included, into single spaces.
func squash(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		b.WriteRune(r)
		space = false
	}
	return b.String()
}

// WarmSearch fills the search cache for every session, so the first search is as fast as the rest.
func (s *Store) WarmSearch() {
	projects, err := s.Projects()
	if err != nil {
		return
	}
	for _, p := range projects {
		for _, sum := range p.Sessions {
			s.searchEntries(filepath.Join(s.root, p.Dir, sum.ID+".jsonl"))
		}
	}
}
