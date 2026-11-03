package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Instruction levels, in the order Claude Code loads them.
const (
	LevelGlobal  = "global"
	LevelParent  = "parent"
	LevelProject = "project"
	LevelLocal   = "local"
)

// Git states of an instruction file.
const (
	GitTracked   = "tracked"
	GitModified  = "tracked · modified"
	GitUntracked = "untracked"
)

// Instruction is a CLAUDE.md-style file Claude Code loads into a project's sessions.
type Instruction struct {
	Path  string
	Level string
	Body  string
	// Git is empty when the file is outside any git repository or git is not installed.
	Git string
}

// instructionFiles lists the instruction files that exist for a project, in load order:
// global, then CLAUDE.md files in the directories above the project, then the project's own.
func (s *Store) instructionFiles(p Project) []Instruction {
	var out []Instruction
	seen := map[string]bool{}
	add := func(level string, paths ...string) {
		for _, path := range paths {
			if seen[path] {
				continue
			}
			if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
				continue
			}
			seen[path] = true
			out = append(out, Instruction{Path: path, Level: level})
		}
	}
	add(LevelGlobal, filepath.Join(s.home(), "CLAUDE.md"))
	add(LevelGlobal, rules(filepath.Join(s.home(), "rules"))...)
	if p.Path == "" || !filepath.IsAbs(p.Path) {
		return out
	}
	for _, dir := range parents(p.Path) {
		add(LevelParent, filepath.Join(dir, "CLAUDE.md"), filepath.Join(dir, "CLAUDE.local.md"))
	}
	add(LevelProject, filepath.Join(p.Path, "CLAUDE.md"), filepath.Join(p.Path, ".claude", "CLAUDE.md"))
	add(LevelLocal, filepath.Join(p.Path, "CLAUDE.local.md"))
	add(LevelProject, rules(filepath.Join(p.Path, ".claude", "rules"))...)
	return out
}

// Instructions returns a project's instruction files with their text and git state.
func (s *Store) Instructions(p Project) []Instruction {
	files := s.instructionFiles(p)
	for i := range files {
		if data, err := os.ReadFile(files[i].Path); err == nil {
			files[i].Body = string(data)
		}
		files[i].Git = gitState(files[i].Path)
	}
	return files
}

// parents lists the directories above path, from the filesystem root down.
func parents(path string) []string {
	var out []string
	for dir := filepath.Dir(filepath.Clean(path)); ; dir = filepath.Dir(dir) {
		out = append(out, dir)
		if filepath.Dir(dir) == dir {
			break
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func rules(dir string) []string {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	sort.Strings(paths)
	return paths
}

// gitState asks git about one file. A clean tracked file gives no status line;
// "??" and "!!" mean untracked and ignored; anything else is a tracked file with changes.
func gitState(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", filepath.Dir(path), "status", "--porcelain", "--ignored", "--", filepath.Base(path))
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	line := strings.TrimRight(string(out), "\n")
	switch {
	case line == "":
		return GitTracked
	case strings.HasPrefix(line, "??"), strings.HasPrefix(line, "!!"):
		return GitUntracked
	}
	return GitModified
}
