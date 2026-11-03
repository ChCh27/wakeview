package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ChCh27/wakeview/internal/export"
	"github.com/ChCh27/wakeview/internal/session"
	"github.com/ChCh27/wakeview/web"
)

type Server struct {
	store    *session.Store
	dec      *session.Decoder
	pages    map[string]*template.Template
	partials *template.Template
	export   *template.Template
	css      template.CSS
	mux      *http.ServeMux
}

var funcs = template.FuncMap{
	"fmtTime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.Local().Format("2006-01-02 15:04")
	},
	"fmtBytes": func(n int64) string {
		switch {
		case n >= 1<<30:
			return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
		case n >= 1<<20:
			return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
		case n >= 1<<10:
			return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
		}
		return fmt.Sprintf("%d B", n)
	},
	"fmtCost": func(usd float64) string {
		return fmt.Sprintf("$%.2f", usd)
	},
	"fmtTokens": func(n int64) string {
		switch {
		case n >= 1_000_000:
			return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
		case n >= 1_000:
			return fmt.Sprintf("%.1fk", float64(n)/1_000)
		}
		return strconv.FormatInt(n, 10)
	},
	"fmtDuration": func(d time.Duration) string {
		d = d.Round(time.Second)
		h, m, sec := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
		switch {
		case h > 0:
			return fmt.Sprintf("%dh %02dm", h, m)
		case m > 0:
			return fmt.Sprintf("%dm %02ds", m, sec)
		}
		return fmt.Sprintf("%ds", sec)
	},
}

func New(store *session.Store) (*Server, error) {
	s := &Server{store: store, dec: session.NewDecoder(), pages: map[string]*template.Template{}, mux: http.NewServeMux()}
	var err error
	s.partials, err = template.New("").Funcs(funcs).ParseFS(web.FS, "templates/partials.html")
	if err != nil {
		return nil, err
	}
	for _, page := range []string{"projects", "sessions", "session", "memories", "memory", "delete", "trash", "search", "agent", "instructions", "global", "delete_project", "help"} {
		t, err := template.New("").Funcs(funcs).ParseFS(web.FS, "templates/base.html", "templates/partials.html", "templates/"+page+".html")
		if err != nil {
			return nil, err
		}
		s.pages[page] = t
	}
	s.export, err = template.New("").Funcs(funcs).ParseFS(web.FS, "templates/partials.html", "templates/export.html")
	if err != nil {
		return nil, err
	}
	css, err := web.FS.ReadFile("static/style.css")
	if err != nil {
		return nil, err
	}
	s.css = template.CSS(css)
	static, err := fs.Sub(web.FS, "static")
	if err != nil {
		return nil, err
	}
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	s.mux.HandleFunc("GET /{$}", s.handleProjects)
	s.mux.HandleFunc("GET /p/{dir}", s.handleSessions)
	s.mux.HandleFunc("GET /p/{dir}/memory", s.handleMemories)
	s.mux.HandleFunc("GET /p/{dir}/instructions", s.handleInstructions)
	s.mux.HandleFunc("GET /instructions", s.handleGlobalInstructions)
	s.mux.HandleFunc("GET /p/{dir}/memory/{file}", s.handleMemory)
	s.mux.HandleFunc("GET /p/{dir}/s/{id}", s.handleSession)
	s.mux.HandleFunc("GET /p/{dir}/s/{id}/events", s.handleEvents)
	s.mux.HandleFunc("GET /p/{dir}/s/{id}/agents/{aid}", s.handleAgent)
	s.mux.HandleFunc("GET /p/{dir}/s/{id}/agents/{aid}/events", s.handleAgentEvents)
	s.mux.HandleFunc("GET /p/{dir}/delete", s.handleDeleteProjectConfirm)
	s.mux.HandleFunc("POST /p/{dir}/delete", sameOrigin(s.handleDeleteProject))
	s.mux.HandleFunc("POST /p/{dir}/s/{id}/rename", sameOrigin(s.handleRename))
	s.mux.HandleFunc("GET /p/{dir}/s/{id}/delete", s.handleDeleteConfirm)
	s.mux.HandleFunc("POST /p/{dir}/s/{id}/delete", sameOrigin(s.handleDelete))
	s.mux.HandleFunc("GET /search", s.handleSearch)
	s.mux.HandleFunc("GET /help", func(w http.ResponseWriter, r *http.Request) { s.render(w, "help", nil) })
	s.mux.HandleFunc("GET /trash", s.handleTrash)
	s.mux.HandleFunc("POST /trash/{tid}/restore", sameOrigin(s.handleRestore))
	s.mux.HandleFunc("POST /trash/{tid}/purge", sameOrigin(s.handlePurge))
	s.mux.HandleFunc("GET /p/{dir}/s/{id}/export.md", s.handleExportMarkdown)
	s.mux.HandleFunc("GET /p/{dir}/s/{id}/export.html", s.handleExportHTML)
	return s, nil
}

// ServeHTTP answers only requests addressed to localhost. A site that points its own domain
// at 127.0.0.1 (DNS rebinding) sends its domain in Host and is refused, so it can't read sessions.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if host != "127.0.0.1" && host != "localhost" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.Projects()
	if err != nil {
		s.fail(w, err)
		return
	}
	var total float64
	var known bool
	for _, p := range projects {
		total += p.CostUSD
		known = known || p.CostKnown
	}
	lp := parseList(r.URL.Query(), projectColumns, "updated")
	shown := filterProjects(projects, lp)
	s.render(w, "projects", map[string]any{
		"Projects":  shown,
		"Total":     len(projects),
		"List":      lp,
		"Headers":   headers("/", lp, projectColumns),
		"CostUSD":   total,
		"CostKnown": known,
	})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.Project(r.PathValue("dir"))
	if err != nil {
		s.fail(w, err)
		return
	}
	lp := parseList(r.URL.Query(), sessionColumns, "updated")
	s.render(w, "sessions", map[string]any{
		"Project":  p,
		"Sessions": filterSessions(p.Sessions, lp),
		"List":     lp,
		"Headers":  headers("/p/"+url.PathEscape(p.Dir), lp, sessionColumns),
	})
}

func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request) {
	dir := r.PathValue("dir")
	p, err := s.store.Project(dir)
	if err != nil {
		s.fail(w, err)
		return
	}
	mems, err := s.store.Memories(dir)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "memories", map[string]any{"Project": p, "Memories": mems, "HasIndex": s.store.HasIndex(dir), "IndexFile": session.IndexFile})
}

type instructionView struct {
	session.Instruction
	HTML template.HTML
}

func (s *Server) handleInstructions(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.Project(r.PathValue("dir"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "instructions", map[string]any{
		"Project": p,
		"Files":   s.instructionViews(s.store.Instructions(p)),
		"Global":  s.store.GlobalCount(),
	})
}

func (s *Server) handleGlobalInstructions(w http.ResponseWriter, r *http.Request) {
	s.render(w, "global", map[string]any{"Files": s.instructionViews(s.store.GlobalInstructions())})
}

func (s *Server) instructionViews(files []session.Instruction) []instructionView {
	var out []instructionView
	for _, in := range files {
		out = append(out, instructionView{Instruction: in, HTML: s.dec.Markdown(in.Body)})
	}
	return out
}

func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	dir := r.PathValue("dir")
	p, err := s.store.Project(dir)
	if err != nil {
		s.fail(w, err)
		return
	}
	m, err := s.store.Memory(dir, r.PathValue("file"))
	if err != nil {
		s.fail(w, err)
		return
	}
	mems, err := s.store.Memories(dir)
	if err != nil {
		s.fail(w, err)
		return
	}
	origin := ""
	if m.OriginSession != "" {
		if _, _, err := s.store.Session(dir, m.OriginSession); err == nil {
			origin = fmt.Sprintf("/p/%s/s/%s", url.PathEscape(dir), url.PathEscape(m.OriginSession))
		}
	}
	s.render(w, "memory", map[string]any{
		"Project": p,
		"Memory":  m,
		"Origin":  origin,
		"HTML":    s.dec.Markdown(linkMemories(m.Body, dir, mems)),
	})
}

var wikiLink = regexp.MustCompile(`\[\[([^\[\]|]+)(?:\|([^\[\]]+))?\]\]`)

// linkMemories turns [[name]] and [[name|label]] into markdown links to the memory with that name.
// A name with no memory becomes a "#broken" link, which the stylesheet greys out.
func linkMemories(body, dir string, mems []session.Memory) string {
	files := map[string]string{}
	for _, m := range mems {
		files[strings.TrimSuffix(m.File, ".md")] = m.File
		if m.Name != "" {
			files[m.Name] = m.File
		}
	}
	return wikiLink.ReplaceAllStringFunc(body, func(match string) string {
		sub := wikiLink.FindStringSubmatch(match)
		name := strings.TrimSpace(sub[1])
		label := strings.TrimSpace(sub[2])
		if label == "" {
			label = name
		}
		if file, ok := files[name]; ok {
			return fmt.Sprintf("[%s](/p/%s/memory/%s)", label, url.PathEscape(dir), url.PathEscape(file))
		}
		return fmt.Sprintf("[%s](#broken)", label)
	})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	dir, id := r.PathValue("dir"), r.PathValue("id")
	p, sum, batch, err := s.loadSession(dir, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	agents, err := s.store.Agents(dir, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	base := sessionURL(dir, id)
	linkAgents(base, &batch)
	active, err := s.store.Active(dir, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "session", map[string]any{
		"Project":  p,
		"Summary":  sum,
		"Active":   active,
		"MaxTitle": session.MaxTitle,
		"Entries":  batch.Entries,
		"Agents":   agents,
		"Base":     base,
		"Events":   fmt.Sprintf("%s/events?offset=%d", base, batch.Offset),
	})
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	dir, id, aid := r.PathValue("dir"), r.PathValue("id"), r.PathValue("aid")
	p, err := s.store.Project(dir)
	if err != nil {
		s.fail(w, err)
		return
	}
	sum, _, err := s.store.Session(dir, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	a, path, err := s.store.Agent(dir, id, aid)
	if err != nil {
		s.fail(w, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.Close()
	batch, err := s.dec.DecodeAgent(f)
	if err != nil {
		s.fail(w, err)
		return
	}
	base := sessionURL(dir, id)
	linkAgents(base, &batch)
	s.render(w, "agent", map[string]any{
		"Project": p,
		"Summary": sum,
		"Agent":   a,
		"Entries": batch.Entries,
		"Base":    base,
		"Events":  fmt.Sprintf("%s/agents/%s/events?offset=%d", base, url.PathEscape(aid), batch.Offset),
	})
}

func sessionURL(dir, id string) string {
	return fmt.Sprintf("/p/%s/s/%s", url.PathEscape(dir), url.PathEscape(id))
}

// linkAgents points results that launched a subagent at its page. Subagents of subagents are
// stored in the same session directory, so one base serves every level.
func linkAgents(base string, b *session.Batch) {
	b.EachResult(func(r *session.Result) {
		if r.AgentID != "" {
			r.AgentURL = base + "/agents/" + url.PathEscape(r.AgentID)
		}
	})
}

func (s *Server) handleDeleteConfirm(w http.ResponseWriter, r *http.Request) {
	dir, id := r.PathValue("dir"), r.PathValue("id")
	p, err := s.store.Project(dir)
	if err != nil {
		s.fail(w, err)
		return
	}
	sum, _, err := s.store.Session(dir, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	items, err := s.store.SessionItems(dir, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	active, err := s.store.Active(dir, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	var total int64
	for _, it := range items {
		total += it.Size
	}
	s.render(w, "delete", map[string]any{
		"Project": p,
		"Summary": sum,
		"Items":   items,
		"Total":   total,
		"Active":  active,
		"Base":    fmt.Sprintf("/p/%s/s/%s", url.PathEscape(dir), url.PathEscape(id)),
	})
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	dir, id := r.PathValue("dir"), r.PathValue("id")
	if err := s.store.Rename(dir, id, r.FormValue("title")); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, sessionURL(dir, id), http.StatusSeeOther)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	dir := r.PathValue("dir")
	if err := s.store.Delete(dir, r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/p/"+url.PathEscape(dir), http.StatusSeeOther)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	opt := session.SearchOptions{
		Query:   strings.TrimSpace(r.URL.Query().Get("q")),
		Tools:   r.URL.Query().Get("tools") == "1",
		Project: r.URL.Query().Get("project"),
	}
	projects, err := s.store.Projects()
	if err != nil {
		s.fail(w, err)
		return
	}
	start := time.Now()
	sessions, memories, err := s.store.Search(opt)
	if err != nil {
		s.fail(w, err)
		return
	}
	hits := 0
	for _, sh := range sessions {
		hits += sh.Count
	}
	s.render(w, "search", map[string]any{
		"Options":  opt,
		"Projects": projects,
		"Sessions": sessions,
		"Memories": memories,
		"Hits":     hits,
		"Elapsed":  time.Since(start).Round(time.Millisecond),
	})
}

func (s *Server) handleDeleteProjectConfirm(w http.ResponseWriter, r *http.Request) {
	dir := r.PathValue("dir")
	p, err := s.store.Project(dir)
	if err != nil {
		s.fail(w, err)
		return
	}
	items, err := s.store.ProjectItems(dir)
	if err != nil {
		s.fail(w, err)
		return
	}
	var total int64
	for _, it := range items {
		total += it.Size
	}
	s.render(w, "delete_project", map[string]any{
		"Project": p,
		"Items":   items,
		"Total":   total,
		"Active":  s.store.ActiveSessions(p),
	})
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteProject(r.PathValue("dir")); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Trash()
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "trash", items)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.Restore(r.PathValue("tid"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if t.Kind == session.KindTrashProject {
		http.Redirect(w, r, "/p/"+url.PathEscape(t.ProjectDir), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, sessionURL(t.ProjectDir, t.SessionID), http.StatusSeeOther)
}

func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Purge(r.PathValue("tid")); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/trash", http.StatusSeeOther)
}

// sameOrigin rejects requests that did not come from a wakeview page: another site open in the
// browser could otherwise post a form to localhost.
func sameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok := true
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			ok = ok && err == nil && u.Host == r.Host
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
			ok = ok && site == "same-origin"
		}
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleExportMarkdown(w http.ResponseWriter, r *http.Request) {
	p, sum, entries, err := s.loadExport(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	var buf bytes.Buffer
	export.Markdown(&buf, p, sum, entries)
	s.download(w, export.FileName(sum)+".md", "text/markdown; charset=utf-8", &buf)
}

func (s *Server) handleExportHTML(w http.ResponseWriter, r *http.Request) {
	p, sum, entries, err := s.loadExport(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	var buf bytes.Buffer
	err = s.export.ExecuteTemplate(&buf, "export", map[string]any{
		"Project": p,
		"Summary": sum,
		"Entries": entries,
		"CSS":     s.css,
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.download(w, export.FileName(sum)+".html", "text/html; charset=utf-8", &buf)
}

// loadExport reads a session for export. With noresults=1 tool results are left out.
func (s *Server) loadExport(r *http.Request) (session.Project, session.Summary, []session.Entry, error) {
	p, sum, batch, err := s.loadSession(r.PathValue("dir"), r.PathValue("id"))
	if err != nil {
		return p, sum, nil, err
	}
	if r.URL.Query().Get("noresults") == "1" {
		export.OmitResults(batch.Entries)
	}
	return p, sum, batch.Entries, nil
}

func (s *Server) loadSession(dir, id string) (session.Project, session.Summary, session.Batch, error) {
	p, err := s.store.Project(dir)
	if err != nil {
		return p, session.Summary{}, session.Batch{}, err
	}
	sum, path, err := s.store.Session(dir, id)
	if err != nil {
		return p, sum, session.Batch{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return p, sum, session.Batch{}, err
	}
	defer f.Close()
	batch, err := s.dec.Decode(f)
	return p, sum, batch, err
}

func (s *Server) download(w http.ResponseWriter, name, contentType string, body *bytes.Buffer) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	body.WriteTo(w)
}

type update struct {
	Append  string         `json:"append,omitempty"`
	Results []resultUpdate `json:"results,omitempty"`
}

type resultUpdate struct {
	ID    string `json:"id"`
	HTML  string `json:"html"`
	Error bool   `json:"error"`
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	dir, id := r.PathValue("dir"), r.PathValue("id")
	_, path, err := s.store.Session(dir, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.stream(w, r, path, sessionURL(dir, id), s.dec.Decode)
}

func (s *Server) handleAgentEvents(w http.ResponseWriter, r *http.Request) {
	dir, id := r.PathValue("dir"), r.PathValue("id")
	_, path, err := s.store.Agent(dir, id, r.PathValue("aid"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.stream(w, r, path, sessionURL(dir, id), s.dec.DecodeAgent)
}

// stream sends entries appended to the file at path after the given offset.
// The offset doubles as the SSE event id, so a reconnecting browser resumes where it stopped.
func (s *Server) stream(w http.ResponseWriter, r *http.Request, path, base string, decode func(io.Reader) (session.Batch, error)) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	offsetStr := r.Header.Get("Last-Event-ID")
	if offsetStr == "" {
		offsetStr = r.URL.Query().Get("offset")
	}
	offset, err := strconv.ParseInt(offsetStr, 10, 64)
	if err != nil || offset < 0 {
		http.Error(w, "bad offset", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		info, err := os.Stat(path)
		if err != nil {
			return
		}
		if info.Size() < offset {
			// The file was rewritten; the page no longer matches it.
			fmt.Fprint(w, "event: reload\ndata: {}\n\n")
			flusher.Flush()
			return
		}
		if info.Size() == offset {
			continue
		}
		batch, err := readFrom(path, offset, decode)
		if err != nil {
			log.Printf("events %s: %v", path, err)
			return
		}
		linkAgents(base, &batch)
		if batch.Offset == 0 {
			continue
		}
		offset += batch.Offset
		var u update
		if len(batch.Entries) > 0 {
			var buf bytes.Buffer
			if err := s.partials.ExecuteTemplate(&buf, "entries", batch.Entries); err != nil {
				log.Printf("events %s: %v", path, err)
				return
			}
			u.Append = buf.String()
		}
		for _, res := range batch.Results {
			var buf bytes.Buffer
			if err := s.partials.ExecuteTemplate(&buf, "result", res); err != nil {
				log.Printf("events %s: %v", path, err)
				return
			}
			u.Results = append(u.Results, resultUpdate{ID: res.ToolID, HTML: buf.String(), Error: res.IsError})
		}
		data, _ := json.Marshal(u)
		fmt.Fprintf(w, "id: %d\ndata: %s\n\n", offset, data)
		flusher.Flush()
	}
}

func readFrom(path string, offset int64, decode func(io.Reader) (session.Batch, error)) (session.Batch, error) {
	f, err := os.Open(path)
	if err != nil {
		return session.Batch{}, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, 0); err != nil {
		return session.Batch{}, err
	}
	return decode(f)
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	var buf bytes.Buffer
	if err := s.pages[page].ExecuteTemplate(&buf, "base", data); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, session.ErrNotFound) {
		http.NotFound(w, nil)
		return
	}
	if errors.Is(err, session.ErrBadTitle) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, session.ErrActive) || errors.Is(err, session.ErrConflict) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	log.Print(err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
