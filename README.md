# wakeview

Browse your Claude Code sessions in a local web UI.

wakeview reads the session files Claude Code keeps in `~/.claude/projects` and serves them as web pages on localhost:

- **Projects**: every project with its session count, memory count, cost and last activity. Projects whose directory no longer exists on disk are marked.
- **Sessions**: each session's title, start time, last update, message count and cost.
- **Stats**: per session, the cost, duration, lines changed and tokens by model. Costs are Claude Code's own estimate at API prices, not what a subscription is billed.
- **Transcript**: your prompts and Claude's replies (rendered markdown). Tool calls and thinking are collapsed; open a tool call to see its input and result.
- **Memory**: a project's memory files with their type, description and the session that created them. `[[name]]` links lead to the memory they name; links to missing memories are greyed out.
- **Live**: an open transcript picks up new messages while the session is still running.
- **Export**: download a session as Markdown or as a self-contained HTML file. Tick "without tool results" to leave command output, which may contain secrets, out of the file.

Subagent transcripts are not shown.

## Install

Requires Go 1.26 or newer.

```sh
go install github.com/ChCh27/wakeview@latest
```

## Run

```sh
wakeview
```

Then open http://127.0.0.1:7777.

| Flag | Default | |
|---|---|---|
| `--port` | `7777` | Port to listen on. wakeview binds to 127.0.0.1 only. |
| `--dir` | `~/.claude/projects` | Claude Code projects directory. |

wakeview only reads the session files; it never modifies them.

## License

[MIT](LICENSE)

wakeview is not affiliated with Anthropic.
