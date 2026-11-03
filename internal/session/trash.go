package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ActiveWindow is how recently a session file may have changed before deletion is refused:
// a session that is still running would just write its file again.
const ActiveWindow = 5 * time.Minute

var (
	ErrActive   = errors.New("session changed less than 5 minutes ago and may still be running")
	ErrConflict = errors.New("a file already exists where the session would be restored")
)

const manifestFile = "manifest.json"

// Item is one file or directory that belongs to a session.
type Item struct {
	// Path is where the item lives outside the trash.
	Path string
	// Stored is the item's name inside its trash entry.
	Stored string
	Size   int64
}

// Trashed is a session moved to wakeview's trash.
type Trashed struct {
	ID          string
	SessionID   string
	ProjectDir  string
	ProjectName string
	Title       string
	DeletedAt   time.Time
	Items       []Item
	Size        int64 `json:"-"`
}

// home is the Claude Code config directory, the parent of the projects directory.
func (s *Store) home() string { return filepath.Dir(s.root) }

func (s *Store) trashDir() string { return filepath.Join(s.home(), "wakeview-trash") }

// SessionItems lists what deleting a session would move: its transcript, its own directory
// (subagents, large tool results), its file-history snapshots and its session-env.
func (s *Store) SessionItems(dir, id string) ([]Item, error) {
	if _, _, err := s.Session(dir, id); err != nil {
		return nil, err
	}
	candidates := []Item{
		{Path: filepath.Join(s.root, dir, id+".jsonl"), Stored: "transcript.jsonl"},
		{Path: filepath.Join(s.root, dir, id), Stored: "session"},
		{Path: filepath.Join(s.home(), "file-history", id), Stored: "file-history"},
		{Path: filepath.Join(s.home(), "session-env", id), Stored: "session-env"},
	}
	var out []Item
	for _, it := range candidates {
		size, err := diskSize(it.Path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		it.Size = size
		out = append(out, it)
	}
	return out, nil
}

// Active reports whether the session file changed within ActiveWindow.
func (s *Store) Active(dir, id string) (bool, error) {
	_, path, err := s.Session(dir, id)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return time.Since(info.ModTime()) < ActiveWindow, nil
}

// Delete moves a session's items into a new trash entry.
func (s *Store) Delete(dir, id string) error {
	active, err := s.Active(dir, id)
	if err != nil {
		return err
	}
	if active {
		return ErrActive
	}
	p, err := s.Project(dir)
	if err != nil {
		return err
	}
	sum, _, err := s.Session(dir, id)
	if err != nil {
		return err
	}
	items, err := s.SessionItems(dir, id)
	if err != nil {
		return err
	}
	t := Trashed{
		ID:          fmt.Sprintf("%d-%s", time.Now().UnixMilli(), id),
		SessionID:   id,
		ProjectDir:  dir,
		ProjectName: p.Name(),
		Title:       sum.Title,
		DeletedAt:   time.Now(),
		Items:       items,
	}
	entry := filepath.Join(s.trashDir(), t.ID)
	if err := os.MkdirAll(entry, 0o700); err != nil {
		return err
	}
	// The manifest goes first, so an interrupted delete can still be restored.
	if err := writeManifest(entry, t); err != nil {
		return err
	}
	for i, it := range items {
		if err := os.Rename(it.Path, filepath.Join(entry, it.Stored)); err != nil {
			for _, done := range items[:i] {
				os.Rename(filepath.Join(entry, done.Stored), done.Path)
			}
			os.RemoveAll(entry)
			return err
		}
	}
	return nil
}

// Trash lists trash entries, most recently deleted first.
func (s *Store) Trash() ([]Trashed, error) {
	ents, err := os.ReadDir(s.trashDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Trashed
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		t, err := s.trashed(e.Name())
		if err != nil {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt.After(out[j].DeletedAt) })
	return out, nil
}

// Restore moves a trash entry's items back. Nothing is moved if any original path is taken.
func (s *Store) Restore(trashID string) (Trashed, error) {
	t, err := s.trashed(trashID)
	if err != nil {
		return t, err
	}
	entry := filepath.Join(s.trashDir(), trashID)
	for _, it := range t.Items {
		if _, err := os.Lstat(it.Path); err == nil {
			return t, ErrConflict
		}
	}
	for _, it := range t.Items {
		src := filepath.Join(entry, it.Stored)
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(it.Path), 0o700); err != nil {
			return t, err
		}
		if err := os.Rename(src, it.Path); err != nil {
			return t, err
		}
	}
	return t, os.RemoveAll(entry)
}

// Purge deletes a trash entry for good.
func (s *Store) Purge(trashID string) error {
	if _, err := s.trashed(trashID); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.trashDir(), trashID))
}

func (s *Store) trashed(trashID string) (Trashed, error) {
	if !validName(trashID) {
		return Trashed{}, ErrNotFound
	}
	entry := filepath.Join(s.trashDir(), trashID)
	data, err := os.ReadFile(filepath.Join(entry, manifestFile))
	if err != nil {
		return Trashed{}, ErrNotFound
	}
	var t Trashed
	if err := json.Unmarshal(data, &t); err != nil {
		return Trashed{}, err
	}
	t.ID = trashID
	for _, it := range t.Items {
		if !validName(it.Stored) {
			return Trashed{}, fmt.Errorf("trash %s: bad item name %q", trashID, it.Stored)
		}
		t.Size += it.Size
	}
	return t, nil
}

func writeManifest(entry string, t Trashed) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(entry, manifestFile), data, 0o600)
}

// diskSize is the total size of the regular files at path.
func diskSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}
