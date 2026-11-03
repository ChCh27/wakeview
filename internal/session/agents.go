package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Agent is a subagent transcript stored under a session's subagents directory.
type Agent struct {
	ID          string
	Type        string
	Description string
	Messages    int
	Updated     time.Time
}

func (a Agent) Title() string {
	if a.Description != "" {
		return a.Description
	}
	return "agent-" + a.ID
}

type agentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
}

func (s *Store) agentsDir(dir, id string) string {
	return filepath.Join(s.root, dir, id, "subagents")
}

// Agents lists a session's subagents, oldest first, the order they ran in.
func (s *Store) Agents(dir, id string) ([]Agent, error) {
	if _, _, err := s.Session(dir, id); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(s.agentsDir(dir, id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Agent
	for _, f := range files {
		name := f.Name()
		if f.IsDir() || !strings.HasPrefix(name, "agent-") || filepath.Ext(name) != ".jsonl" {
			continue
		}
		a, _, err := s.Agent(dir, id, strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl"))
		if err != nil {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.Before(out[j].Updated) })
	return out, nil
}

// Agent returns one subagent and the path of its transcript.
func (s *Store) Agent(dir, id, agentID string) (Agent, string, error) {
	if _, _, err := s.Session(dir, id); err != nil {
		return Agent{}, "", err
	}
	if !safeID(agentID) {
		return Agent{}, "", ErrNotFound
	}
	base := filepath.Join(s.agentsDir(dir, id), "agent-"+agentID)
	path := base + ".jsonl"
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Agent{}, "", ErrNotFound
	}
	a := Agent{ID: agentID, Updated: info.ModTime()}
	if data, err := os.ReadFile(base + ".meta.json"); err == nil {
		var m agentMeta
		if json.Unmarshal(data, &m) == nil {
			a.Type, a.Description = m.AgentType, m.Description
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return a, path, err
	}
	defer f.Close()
	batch, err := (&Decoder{}).decode(f, false, true)
	if err != nil {
		return a, path, err
	}
	for _, e := range batch.Entries {
		if e.Kind == KindPrompt || e.Kind == KindText {
			a.Messages++
		}
	}
	return a, path, nil
}
