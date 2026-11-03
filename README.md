# wakeview

Browse your Claude Code sessions in a local web UI.

wakeview reads the session files Claude Code keeps in `~/.claude/projects` and serves them as web pages on localhost:

- **Projects**: every project with its session count, memory count, cost and last activity. Projects whose directory no longer exists on disk are marked.
- **Sessions**: each session's title, start time, last update, message count and cost.
- **Stats**: per session, the cost, duration, lines changed and tokens by model. Costs are Claude Code's own estimate at API prices, not what a subscription is billed.
- **Transcript**: your prompts and Claude's replies (rendered markdown). Tool calls and thinking are collapsed; open a tool call to see its input and result.
- **Subagents**: a session lists its subagents, and the tool call that launched one links to its transcript.
- **Memory**: a project's memory files with their type, description and the session that created them. `[[name]]` links lead to the memory they name; links to missing memories are greyed out.
- **Search**: case-insensitive search across prompts, replies and memory, optionally also tool calls and within one project. All words must occur in the same message; a result opens the session at that message.
- **Live**: an open transcript picks up new messages while the session is still running.
- **Export**: download a session as Markdown or as a self-contained HTML file. Tick "without tool results" to leave command output, which may contain secrets, out of the file.

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

wakeview changes files only when you delete a session. A deleted session's transcript, its `<id>/` directory, `file-history/<id>` and `session-env/<id>` are moved to `~/.claude/wakeview-trash/`; the Trash page restores them or deletes them for good. A session changed less than 5 minutes ago can't be deleted, since it may still be running.

## License

[MIT](LICENSE)

wakeview is not affiliated with Anthropic.
