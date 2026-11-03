package session

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

const MaxTitle = 200

var ErrBadTitle = errors.New("title must not be empty")

type customTitleRecord struct {
	Type        string `json:"type"`
	CustomTitle string `json:"customTitle"`
	SessionID   string `json:"sessionId"`
}

type agentNameRecord struct {
	Type      string `json:"type"`
	AgentName string `json:"agentName"`
	SessionID string `json:"sessionId"`
}

// CleanTitle turns line breaks into spaces, trims the title and cuts it to MaxTitle runes.
func CleanTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if r := []rune(title); len(r) > MaxTitle {
		title = strings.TrimSpace(string(r[:MaxTitle]))
	}
	return title
}

// Rename appends the same custom-title and agent-name records Claude Code writes for /rename,
// so both wakeview and Claude Code show the new name. The file keeps its modification time:
// a rename is not activity, and a fresh mtime would make the session look like it is running.
func (s *Store) Rename(dir, id, title string) error {
	title = CleanTitle(title)
	if title == "" {
		return ErrBadTitle
	}
	active, err := s.Active(dir, id)
	if err != nil {
		return err
	}
	if active {
		return ErrActive
	}
	_, path, err := s.Session(dir, id)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	var out []byte
	if !endsWithNewline(f) {
		out = append(out, '\n')
	}
	for _, rec := range []any{
		customTitleRecord{Type: "custom-title", CustomTitle: title, SessionID: id},
		agentNameRecord{Type: "agent-name", AgentName: title, SessionID: id},
	} {
		line, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		out = append(append(out, line...), '\n')
	}
	if _, err := f.Write(out); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(path, time.Time{}, info.ModTime())
}

func endsWithNewline(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return true
	}
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, info.Size()-1); err != nil && err != io.EOF {
		return true
	}
	return b[0] == '\n'
}
