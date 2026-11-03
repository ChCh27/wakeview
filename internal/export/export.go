// Package export turns a session into a file to download.
package export

import (
	"fmt"
	"html"
	"io"
	"strings"
	"unicode"

	"github.com/ChCh27/wakeview/internal/session"
)

// OmitResults replaces every tool result with an omitted marker, so command output stays out of the file.
func OmitResults(entries []session.Entry) {
	for _, e := range entries {
		if e.Tool != nil {
			e.Tool.Result = &session.Result{ToolID: e.Tool.ID, Omitted: true}
		}
	}
}

// Markdown writes the session as Markdown. Tool calls, thinking and compaction summaries
// go into <details> blocks, which GitHub and most viewers show collapsed.
func Markdown(w io.Writer, p session.Project, sum session.Summary, entries []session.Entry) {
	fmt.Fprintf(w, "# %s\n\n", sum.Title)
	if !sum.Started.IsZero() {
		fmt.Fprintf(w, "- Date: %s\n", sum.Started.Local().Format("2006-01-02 15:04"))
	}
	fmt.Fprintf(w, "- Project: `%s`\n", p.Name())
	fmt.Fprintf(w, "- Session: `%s`\n", sum.ID)
	if sum.Stats.Known {
		fmt.Fprintf(w, "- Cost (API est.): $%.2f\n", sum.Stats.CostUSD)
	}
	fmt.Fprint(w, "\n---\n")

	speaker := ""
	for _, e := range entries {
		who := "Claude"
		if e.Kind == session.KindPrompt {
			who = "You"
		}
		if who != speaker && e.Kind != session.KindCompact {
			fmt.Fprintf(w, "\n### %s\n", who)
			speaker = who
		}
		switch e.Kind {
		case session.KindPrompt:
			if e.Text != "" {
				fmt.Fprintf(w, "\n%s\n", e.Text)
			}
			writeImages(w, e.Images)
		case session.KindText:
			fmt.Fprintf(w, "\n%s\n", strings.TrimSpace(e.Text))
		case session.KindThinking:
			fmt.Fprintf(w, "\n<details>\n<summary>Thinking</summary>\n\n%s\n\n</details>\n", strings.TrimSpace(e.Text))
		case session.KindCompact:
			fmt.Fprintf(w, "\n<details>\n<summary>Context compacted</summary>\n\n%s\n\n</details>\n", strings.TrimSpace(e.Text))
			speaker = ""
		case session.KindTool:
			writeTool(w, e.Tool)
		}
	}
}

func writeTool(w io.Writer, t *session.Tool) {
	summary := t.Name
	if t.Hint != "" {
		summary += ": " + t.Hint
	}
	fmt.Fprintf(w, "\n<details>\n<summary>%s</summary>\n", html.EscapeString(summary))
	for _, f := range t.Input {
		if f.Key != "" {
			fmt.Fprintf(w, "\n**%s**\n", f.Key)
		}
		writeCode(w, f.Value)
	}
	switch r := t.Result; {
	case r == nil:
		fmt.Fprint(w, "\n_No result yet._\n")
	case r.Omitted:
		fmt.Fprint(w, "\n_Result omitted._\n")
	default:
		if r.IsError {
			fmt.Fprint(w, "\n**Result (error)**\n")
		} else {
			fmt.Fprint(w, "\n**Result**\n")
		}
		if r.Text != "" {
			writeCode(w, r.Text)
		}
		writeImages(w, r.Images)
	}
	fmt.Fprint(w, "\n</details>\n")
}

// writeCode writes s as a fenced block whose fence is longer than any backtick run inside s.
func writeCode(w io.Writer, s string) {
	longest, run := 0, 0
	for _, c := range s {
		if c == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	fmt.Fprintf(w, "\n%s\n%s\n%s\n", fence, strings.TrimRight(s, "\n"), fence)
}

func writeImages(w io.Writer, images []session.Image) {
	for _, img := range images {
		if u := img.URL(); u != "" {
			fmt.Fprintf(w, "\n![](%s)\n", u)
		}
	}
}

// FileName returns "<date>-<transliterated title>", without an extension.
func FileName(sum session.Summary) string {
	name := slug(sum.Title)
	if name == "" {
		name = sum.ID
		if len(name) > 8 {
			name = name[:8]
		}
	}
	t := sum.Started
	if t.IsZero() {
		t = sum.Updated
	}
	return t.Local().Format("2006-01-02") + "-" + name
}

var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh",
	'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o",
	'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "kh", 'ц': "ts",
	'ч': "ch", 'ш': "sh", 'щ': "shch", 'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu",
	'я': "ya",
}

const maxSlug = 60

// slug lowercases s, transliterates Cyrillic and joins the remaining Latin words and digits with dashes.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(s) {
		if t, ok := translit[c]; ok {
			b.WriteString(t)
			dash = false
			continue
		}
		if c < unicode.MaxASCII && (unicode.IsLetter(c) || unicode.IsDigit(c)) {
			b.WriteRune(c)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := b.String()
	if len(out) > maxSlug {
		out = out[:maxSlug]
		if i := strings.LastIndexByte(out, '-'); i > maxSlug/2 {
			out = out[:i]
		}
	}
	return strings.Trim(out, "-")
}
