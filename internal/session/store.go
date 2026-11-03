package session

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("not found")

type Summary struct {
	ID       string
	Title    string
	Cwd      string
	Messages int
	Started  time.Time
	Updated  time.Time
	Stats    Stats
}

type Project struct {
	// Dir is the directory name under the projects root, e.g. "-home-me-code-app".
	Dir string
	// Path is the working directory the sessions were started in, taken from the records.
	Path     string
	Missing  bool
	Sessions []Summary
	Updated  time.Time
	// CostUSD sums the sessions that have cost data; CostKnown is false when none do.
	CostUSD   float64
	CostKnown bool
	// Memories is the number of memory files, not counting MEMORY.md.
	Memories int
}

func (p Project) Name() string {
	if p.Path != "" {
		return p.Path
	}
	return p.Dir
}

// Store reads projects and sessions from a Claude Code projects directory.
type Store struct {
	root  string
	mu    sync.Mutex
	cache map[string]cachedSummary
}

type cachedSummary struct {
	mod  time.Time
	size int64
	sum  Summary
}

func NewStore(root string) *Store {
	return &Store{root: root, cache: map[string]cachedSummary{}}
}

func (s *Store) Projects() ([]Project, error) {
	ents, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	var out []Project
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p, err := s.Project(e.Name())
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

func (s *Store) Project(dir string) (Project, error) {
	if !validName(dir) {
		return Project{}, ErrNotFound
	}
	files, err := os.ReadDir(filepath.Join(s.root, dir))
	if err != nil {
		return Project{}, ErrNotFound
	}
	p := Project{Dir: dir, Memories: s.memoryCount(dir)}
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		sum, err := s.summary(filepath.Join(s.root, dir, f.Name()), info)
		if err != nil {
			continue
		}
		p.Sessions = append(p.Sessions, sum)
	}
	sort.Slice(p.Sessions, func(i, j int) bool { return p.Sessions[i].Updated.After(p.Sessions[j].Updated) })
	if len(p.Sessions) > 0 {
		p.Updated = p.Sessions[0].Updated
	}
	for _, sum := range p.Sessions {
		if sum.Stats.Known {
			p.CostUSD += sum.Stats.CostUSD
			p.CostKnown = true
		}
	}
	for _, sum := range p.Sessions {
		if sum.Cwd != "" {
			p.Path = sum.Cwd
			break
		}
	}
	if p.Path != "" {
		if _, err := os.Stat(p.Path); errors.Is(err, fs.ErrNotExist) {
			p.Missing = true
		}
	}
	return p, nil
}

// Session returns the summary and file path of one session.
func (s *Store) Session(dir, id string) (Summary, string, error) {
	if !validName(dir) || !validName(id) {
		return Summary{}, "", ErrNotFound
	}
	path := filepath.Join(s.root, dir, id+".jsonl")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Summary{}, "", ErrNotFound
	}
	sum, err := s.summary(path, info)
	return sum, path, err
}

func (s *Store) summary(path string, info fs.FileInfo) (Summary, error) {
	s.mu.Lock()
	c, ok := s.cache[path]
	s.mu.Unlock()
	if ok && c.mod.Equal(info.ModTime()) && c.size == info.Size() {
		return c.sum, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return Summary{}, err
	}
	defer f.Close()
	sum, err := summarize(f)
	if err != nil {
		return Summary{}, err
	}
	sum.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	sum.Updated = info.ModTime()
	s.mu.Lock()
	s.cache[path] = cachedSummary{mod: info.ModTime(), size: info.Size(), sum: sum}
	s.mu.Unlock()
	return sum, nil
}

func summarize(f *os.File) (Summary, error) {
	var sum Summary
	var custom, ai, first string
	costs := map[int64]costState{}
	_, err := readRecords(f, func(rec *record) {
		if sum.Cwd == "" && rec.Cwd != "" {
			sum.Cwd = rec.Cwd
		}
		if sum.Started.IsZero() && rec.Timestamp != "" {
			sum.Started = parseTime(rec.Timestamp)
		}
		if _, ok := midTurnText(rec); ok {
			sum.Messages++
			return
		}
		switch rec.Type {
		case "cost-state":
			costs[rec.StartTime] = rec.costState
		case "custom-title":
			custom = rec.CustomTitle
		case "ai-title":
			ai = rec.AITitle
		case "user":
			if rec.IsMeta || rec.IsSidechain || rec.IsCompactSummary || rec.Message == nil {
				return
			}
			texts, images, _ := splitUser(decodeContent(rec.Message.Content))
			if len(texts) == 0 && len(images) == 0 {
				return
			}
			sum.Messages++
			text := strings.Join(texts, " ")
			if first == "" && !strings.HasPrefix(strings.TrimSpace(text), "<") {
				first = text
			}
		case "assistant":
			if rec.IsSidechain || rec.Message == nil {
				return
			}
			for _, bl := range decodeContent(rec.Message.Content) {
				if bl.Type == "text" && strings.TrimSpace(bl.Text) != "" {
					sum.Messages++
				}
			}
		}
	})
	for _, t := range []string{custom, ai, oneLine(first, 120)} {
		if strings.TrimSpace(t) != "" {
			sum.Title = t
			break
		}
	}
	if sum.Title == "" {
		sum.Title = "(no prompt)"
	}
	sum.Stats = statsFrom(costs)
	return sum, err
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}
