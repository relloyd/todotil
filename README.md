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
  `,` opens settings and `?` lists every key.
- **Add:** `a` opens the dialog. The first line is the title, then a blank line and an optional body.
  Lines like `- [ ] call Sam` become child todos. The entry goes to the view you
  are in. Use `alt+n` / `alt+x` / `alt+l` / `alt+j` to send it to Now / Next / Later / Journal
  instead. `enter` saves, `shift+enter` or `ctrl+j` adds a new line.
- **Work:** `x` done · `m` then `n`/`x`/`l`/`j` to move · `e` edit · `enter` details
  · `>`/`<` indent/outdent · `shift+↑/↓` or `alt+k/j` reorder · `K`/`J` top/bottom
  · `u` undo.
- **Navigate:** `j`/`k`, `g`/`G`, `p` parent, `c` first child, `ctrl+o` back.
- **History:** `s` switches between created and completed date, `r` reverses the order.
- **Copy:** `y` copies the item as Markdown. `v` releases the mouse so you can
  select text.

## Files

| Path | Contents |
|---|---|
| `config.toml` | settings, including the History sort |
| `keys.toml` | key bindings (also editable from settings) |
| `theme.toml` | colour overrides on top of the chosen theme |
| `data/events-*.jsonl` | the append-only database |
| `backups/` | daily copies, the last 10 days kept |

See [AGENTS.md](AGENTS.md) for architecture and development notes.
