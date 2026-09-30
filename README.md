# todotil

A terminal todo and meeting-notes tracker built around a **Now / Next / Later**
workflow, with a browsable History, undo, mouse support and plain-text
storage.

```sh
task install   # checks everything, then installs `todotil`
todotil        # data lives in ~/.config/todotil (override with --home or TODOTIL_HOME)
```

## Using it

- **Views:** `1` Now · `2` Next · `3` Later · `4` History, or `tab` to cycle.
  `,` opens settings and `?` lists every key. In either, `/` filters the list by name, description or key (`ctrl+x`).
- **Add:** `a` opens the dialog. The first line is the title, then a blank line and an optional body.
  Lines like `- [ ] call Sam` become child todos. `[x]` marks one done and `[-]` rejected. The entry goes to the view you
  are in. Use `alt+n` / `alt+x` / `alt+l` / `alt+j` to send it to Now / Next / Later / Journal
  instead. `enter` saves, `shift+enter` or `ctrl+j` adds a new line.
- **Work:** `x` done/reopen · `ctrl+x` reject · `m` then `n`/`x`/`l`/`j` to move · `e` edit · `enter` details
  · `>`/`<` indent/outdent · `shift+↑/↓` or `alt+k/j` reorder · `K`/`J` top/bottom
  · `u` undo · `U` unassign an agent.
- **Navigate:** `j`/`k`, `g`/`G`, `p` parent, `c` first child, `ctrl+o` back.
- **History:** `f` cycles All / Completed / Rejected / Journal; `s` switches between
  created and completed/rejected date, `r` reverses the order.
- **Copy:** `y` copies the item as Markdown. `v` releases the mouse so you can
  select text.

## AI agents

Agents can take work from your lists and report back while the TUI is open:

```sh
todotil claim next --as claude-1 --json      # returns a claim ID like c-3f9a…
todotil note  c-3f9a… "tests passing" --json
todotil done  c-3f9a… --json
```

Claimed items show `@claude-1 2h` in the lists, and `U` unassigns one.
`todotil help agents` prints instructions to paste into another repo's
`AGENTS.md`. The full interface and its exit codes are in
[docs/agent-cli.md](docs/agent-cli.md).

## Files

| Path | Contents |
|---|---|
| `config.toml` | settings, including the History sort |
| `keys.toml` | key bindings (also editable from settings) |
| `theme.toml` | colour overrides on top of the chosen theme |
| `data/events-*.jsonl` | the append-only database |
| `backups/` | daily copies, the last 10 days kept |

See [AGENTS.md](AGENTS.md) for architecture and development notes.
