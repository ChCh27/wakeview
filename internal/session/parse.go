package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"html/template"
	"io"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// record is one line of a session .jsonl file. Only the fields wakeview uses are decoded.
type record struct {
	Type             string   `json:"type"`
	Timestamp        string   `json:"timestamp"`
	Cwd              string   `json:"cwd"`
	IsMeta           bool     `json:"isMeta"`
	IsSidechain      bool     `json:"isSidechain"`
	IsCompactSummary bool     `json:"isCompactSummary"`
	Message          *message `json:"message"`
	AITitle          string   `json:"aiTitle"`
	CustomTitle      string   `json:"customTitle"`
	// Operation, Reason and Content describe queue-operation records.
	Operation string          `json:"operation"`
	Reason    string          `json:"reason"`
	Content   json.RawMessage `json:"content"`
	costState
}

// midTurnText returns a message the user sent while Claude was working.
// Such messages are stored only as a queue-operation record, not as a user record.
func midTurnText(rec *record) (string, bool) {
	if rec.Type != "queue-operation" || rec.Operation != "remove" || rec.Reason != "absorbed_mid_turn" {
		return "", false
	}
	var s string
	if json.Unmarshal(rec.Content, &s) != nil || strings.TrimSpace(s) == "" {
		return "", false
	}
	return s, true
}

type message struct {
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	ToolName  string          `json:"tool_name"`
	Source    *imageSource    `json:"source"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type Kind string

const (
	KindPrompt   Kind = "prompt"
	KindText     Kind = "text"
	KindThinking Kind = "thinking"
	KindTool     Kind = "tool"
	KindCompact  Kind = "compact"
)

// Entry is one visible item of a session transcript.
type Entry struct {
	Kind   Kind
	Time   time.Time
	Text   string
	HTML   template.HTML
	Images []Image
	Tool   *Tool
}

type Tool struct {
	ID     string
	Name   string
	Hint   string
	Input  []Field
	Result *Result
}

// Field is one top-level key of a tool input. String values are kept raw so newlines stay readable.
type Field struct {
	Key   string
	Value string
}

type Result struct {
	ToolID  string
	Text    string
	IsError bool
	Images  []Image
	// Omitted marks a result left out of an export.
	Omitted bool
}

type Image struct {
	MediaType string
	Data      string
}

var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

func (i Image) URL() template.URL {
	if !imageTypes[i.MediaType] {
		return ""
	}
	return template.URL("data:" + i.MediaType + ";base64," + i.Data)
}

// Batch is the result of decoding a run of complete lines.
type Batch struct {
	Entries []Entry
	// Results whose tool call is not among Entries (it arrived in an earlier batch).
	Results []Result
	// Offset is the number of bytes consumed from the reader.
	Offset int64
}

type Decoder struct {
	md goldmark.Markdown
}

func NewDecoder() *Decoder {
	return &Decoder{md: goldmark.New(goldmark.WithExtensions(extension.GFM))}
}

func (d *Decoder) Decode(r io.Reader) (Batch, error) {
	var b Batch
	tools := map[string]*Tool{}
	n, err := readRecords(r, func(rec *record) {
		ts := parseTime(rec.Timestamp)
		if text, ok := midTurnText(rec); ok {
			b.Entries = append(b.Entries, Entry{Kind: KindPrompt, Time: ts, Text: text})
			return
		}
		if rec.IsSidechain || rec.Message == nil {
			return
		}
		switch rec.Type {
		case "user":
			d.user(&b, tools, rec, ts)
		case "assistant":
			d.assistant(&b, tools, rec, ts)
		}
	})
	b.Offset = n
	return b, err
}

func (d *Decoder) user(b *Batch, tools map[string]*Tool, rec *record, ts time.Time) {
	if rec.IsMeta {
		return
	}
	bs := decodeContent(rec.Message.Content)
	if rec.IsCompactSummary {
		b.Entries = append(b.Entries, Entry{Kind: KindCompact, Time: ts, Text: joinText(bs)})
		return
	}
	texts, images, results := splitUser(bs)
	for _, res := range results {
		if t := tools[res.ToolID]; t != nil {
			t.Result = &res
		} else {
			b.Results = append(b.Results, res)
		}
	}
	if len(texts) > 0 || len(images) > 0 {
		b.Entries = append(b.Entries, Entry{Kind: KindPrompt, Time: ts, Text: strings.Join(texts, "\n\n"), Images: images})
	}
}

func (d *Decoder) assistant(b *Batch, tools map[string]*Tool, rec *record, ts time.Time) {
	for _, bl := range decodeContent(rec.Message.Content) {
		switch bl.Type {
		case "text":
			if strings.TrimSpace(bl.Text) == "" {
				continue
			}
			b.Entries = append(b.Entries, Entry{Kind: KindText, Time: ts, Text: bl.Text, HTML: d.render(bl.Text)})
		case "thinking":
			if strings.TrimSpace(bl.Thinking) == "" {
				continue
			}
			b.Entries = append(b.Entries, Entry{Kind: KindThinking, Time: ts, Text: bl.Thinking})
		case "tool_use":
			fs := fields(bl.Input)
			t := &Tool{ID: bl.ID, Name: bl.Name, Input: fs, Hint: hint(fs)}
			tools[bl.ID] = t
			b.Entries = append(b.Entries, Entry{Kind: KindTool, Time: ts, Tool: t})
		}
	}
}

// render converts markdown to HTML. Raw HTML in the source is dropped by goldmark's default renderer.
func (d *Decoder) render(src string) template.HTML {
	var buf bytes.Buffer
	if err := d.md.Convert([]byte(src), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(buf.String())
}

// readRecords calls fn for every complete line in r and returns the number of bytes consumed.
// A trailing line without a newline is left unconsumed: it may still be being written.
func readRecords(r io.Reader, fn func(*record)) (int64, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var n int64
	for {
		line, err := br.ReadBytes('\n')
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		n += int64(len(line))
		var rec record
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		fn(&rec)
	}
}

// splitUser separates a user message into typed text, pasted images and tool results.
func splitUser(bs []block) (texts []string, images []Image, results []Result) {
	for _, bl := range bs {
		switch bl.Type {
		case "text":
			texts = append(texts, bl.Text)
		case "image":
			if img, ok := imageOf(bl); ok {
				images = append(images, img)
			}
		case "tool_result":
			res := Result{ToolID: bl.ToolUseID, IsError: bl.IsError}
			var parts []string
			for _, c := range decodeContent(bl.Content) {
				switch c.Type {
				case "text":
					parts = append(parts, c.Text)
				case "image":
					if img, ok := imageOf(c); ok {
						res.Images = append(res.Images, img)
					}
				case "tool_reference":
					parts = append(parts, "→ "+c.ToolName)
				}
			}
			res.Text = strings.Join(parts, "\n")
			results = append(results, res)
		}
	}
	return texts, images, results
}

// decodeContent accepts message content in both forms: a plain string or a list of blocks.
func decodeContent(raw json.RawMessage) []block {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		return []block{{Type: "text", Text: s}}
	}
	var bs []block
	if json.Unmarshal(raw, &bs) != nil {
		return nil
	}
	return bs
}

func imageOf(bl block) (Image, bool) {
	if bl.Source == nil || bl.Source.Type != "base64" {
		return Image{}, false
	}
	return Image{MediaType: bl.Source.MediaType, Data: bl.Source.Data}, true
}

func joinText(bs []block) string {
	var parts []string
	for _, bl := range bs {
		if bl.Type == "text" {
			parts = append(parts, bl.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// fields lists the top-level keys of a tool input in their original order.
func fields(raw json.RawMessage) []Field {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return []Field{{Value: indent(raw)}}
	}
	var out []Field
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			break
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			out = append(out, Field{Key: key, Value: s})
		} else {
			out = append(out, Field{Key: key, Value: indent(v)})
		}
	}
	return out
}

func indent(raw []byte) string {
	var buf bytes.Buffer
	if json.Indent(&buf, raw, "", "  ") != nil {
		return string(raw)
	}
	return buf.String()
}

var hintKeys = []string{"description", "command", "file_path", "path", "pattern", "query", "url", "prompt", "skill"}

// hint picks a short line from the tool input to show while the call is collapsed.
func hint(fs []Field) string {
	for _, k := range hintKeys {
		for _, f := range fs {
			if f.Key == k && strings.TrimSpace(f.Value) != "" {
				return oneLine(f.Value, 100)
			}
		}
	}
	return ""
}

func oneLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
