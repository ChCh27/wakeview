package session

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// IndexFile is the memory index Claude Code loads into every session.
const IndexFile = "MEMORY.md"

// Memory is one file in a project's memory directory.
type Memory struct {
	File          string
	Name          string
	Description   string
	Type          string
	OriginSession string
	Modified      time.Time
	Body          string
}

func (m Memory) Title() string {
	if m.Name != "" {
		return m.Name
	}
	return strings.TrimSuffix(m.File, ".md")
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
	Metadata    struct {
		Type            string `yaml:"type"`
		OriginSessionID string `yaml:"originSessionId"`
		Modified        string `yaml:"modified"`
	} `yaml:"metadata"`
}

// Memories lists a project's memory files, without the index, newest first.
func (s *Store) Memories(dir string) ([]Memory, error) {
	if !validName(dir) {
		return nil, ErrNotFound
	}
	files, err := os.ReadDir(filepath.Join(s.root, dir, "memory"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Memory
	for _, f := range files {
		if !isMemoryFile(f) || f.Name() == IndexFile {
			continue
		}
		m, err := s.Memory(dir, f.Name())
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// HasIndex reports whether the project has a MEMORY.md.
func (s *Store) HasIndex(dir string) bool {
	info, err := os.Stat(filepath.Join(s.root, dir, "memory", IndexFile))
	return validName(dir) && err == nil && info.Mode().IsRegular()
}

func (s *Store) Memory(dir, file string) (Memory, error) {
	if !validName(dir) || !validName(file) || filepath.Ext(file) != ".md" {
		return Memory{}, ErrNotFound
	}
	path := filepath.Join(s.root, dir, "memory", file)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Memory{}, ErrNotFound
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Memory{}, err
	}
	m := Memory{File: file, Modified: info.ModTime(), Body: string(data)}
	if fm, body, ok := splitFrontmatter(data); ok {
		m.Body = body
		m.Name = fm.Name
		m.Description = fm.Description
		m.Type = fm.Metadata.Type
		if m.Type == "" {
			m.Type = fm.Type
		}
		m.OriginSession = fm.Metadata.OriginSessionID
		if t, err := time.Parse(time.RFC3339Nano, fm.Metadata.Modified); err == nil {
			m.Modified = t
		}
	}
	return m, nil
}

// splitFrontmatter separates a leading "---" YAML block from the body.
func splitFrontmatter(data []byte) (frontmatter, string, bool) {
	var fm frontmatter
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return fm, "", false
	}
	rest := data[len("---\n"):]
	end := bytes.Index(rest, []byte("\n---\n"))
	body := ""
	switch {
	case end >= 0:
		body = string(rest[end+len("\n---\n"):])
	case bytes.HasSuffix(rest, []byte("\n---")):
		end = len(rest) - len("\n---")
	default:
		return fm, "", false
	}
	if yaml.Unmarshal(rest[:end], &fm) != nil {
		return fm, "", false
	}
	return fm, strings.TrimLeft(body, "\n"), true
}

func isMemoryFile(f os.DirEntry) bool {
	return !f.IsDir() && filepath.Ext(f.Name()) == ".md"
}

func (s *Store) memoryCount(dir string) int {
	files, err := os.ReadDir(filepath.Join(s.root, dir, "memory"))
	if err != nil {
		return 0
	}
	n := 0
	for _, f := range files {
		if isMemoryFile(f) && f.Name() != IndexFile {
			n++
		}
	}
	return n
}
