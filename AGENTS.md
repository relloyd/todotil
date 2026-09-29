# AGENTS.md

Guidance for anyone (human or AI agent) working on **todotil**, a Bubble Tea
terminal app for todos and meeting notes with a Now / Next / Later workflow.

## Quick start

```sh
task            # fmt, vet, lint, test (race), build → ./bin/todotil
task run        # run against ./tmp/home, never your real data
task ci         # like `task check` but fails on unformatted code
task install    # full check, then `go install` into $GOBIN / $GOPATH/bin
```

Run `task --list` for everything. Before calling any change done, `task ci`
must pass. Don't skip lint or the race detector.

## Toolchain

- Go **1.27** (`go.mod` says `go 1.27`). Older local toolchains auto-download
  it via `GOTOOLCHAIN=auto`.
- The golangci-lint installed by Homebrew was built with Go 1.26, and it refuses to
  lint a 1.27 module ("Go language version used to build golangci-lint is lower
  than the targeted Go version"). The Taskfile therefore runs a **pinned**
  golangci-lint via `go run …@v2.14.0`. Bump the pin in `Taskfile.yml`; don't
  switch back to the system binary.
- Don't set misspell's `locale: UK` in `.golangci.yml`, because it flags library
  identifiers (`lipgloss.Color`, `image/color`, `lipgloss.Center`). Prose in
  comments and UI uses British spelling ("colour") and the linter accepts it.

## Libraries (use these, not the v1 equivalents)

| Purpose | Module |
|---|---|
| TUI runtime | `charm.land/bubbletea/v2` |
| Styling, layout, overlays | `charm.land/lipgloss/v2` |
| Text input | `charm.land/bubbles/v2/textarea` |
| ANSI-aware width/truncate/wrap | `github.com/charmbracelet/x/ansi` |
| Config files | `github.com/BurntSushi/toml` |
| Instance lock | `github.com/gofrs/flock` |
| Clipboard | `github.com/atotto/clipboard`, falling back to `tea.SetClipboard` (OSC 52) |
| HTML title parsing | `golang.org/x/net/html` |
| Tests | `github.com/stretchr/testify` (`assert` / `require`) |

Bubble Tea v2 notes that caught us out or are easy to forget:

- `View()` returns a `tea.View`. Alt screen, mouse mode and window title are
  **fields on the view** (`v.AltScreen`, `v.MouseMode`), not program options.
- Keys are `tea.KeyPressMsg`. `msg.String()` returns the typed text when there
  is any (`"K"`, `">"`, `"?"`), otherwise the keystroke (`"ctrl+o"`,
  `"shift+up"`, `"alt+j"`). Match against both `String()` and `Keystroke()`
  (see `Model.keyMatches`).
- Basic kitty keyboard disambiguation is always on, so `shift+enter` works in
  modern terminals. `ctrl+j` is the portable newline fallback.
- Paste arrives as `tea.PasteMsg`, not as key presses.
- Overlays use `lipgloss.NewCompositor(NewLayer(base), NewLayer(dlg).X().Y().Z(1)).Render()`.
  The compositor trims trailing spaces, which is harmless.
- `textarea`: set `DynamicHeight`, `MaxHeight` (the viewport rows) **and**
  `MaxContentHeight`. Without `MaxContentHeight`, `MaxHeight` also caps how many
  lines can be typed. Its default keymap binds `alt+l` (lowercase word), which
  clashes with our "classify as Later", so we disable it.
- `tea.Tick` commands sleep. Never execute them in tests (see `runCmd` in
  `ui_test.go`, which is only used on non-blocking commands).

## Layout

```
main.go                   wiring: flags, lock, config, log replay, program
internal/todo             domain: Item, Board (state + view queries), Service (operations, undo), checkbox sync
internal/store            JSONL event log (split files), lock file, backups/prune
internal/config           config.toml, keys.toml (Actions + KeyMap), theme.toml (palettes)
internal/links            URL/Markdown-link detection, page-title fetcher
internal/ui               Bubble Tea model: model.go (dispatch), render.go, editor.go,
                          detail.go, settings.go (settings + help), actions.go, mouse.go, background.go
```

Keep the dependency direction: `ui → todo, store, config, links`. `store → todo`
for the `Event` type only. `todo` imports nothing from this repo.

## Architecture and invariants

**One record per item, event-sourced.** `data/events-NNNNNN.jsonl` is an
append-only log of `put` (full item snapshot), `del`, and `link` (cached page
title) events. Every file starts with a `meta` line holding the format
version. The board is rebuilt by replaying all files in name order, last
write wins. Rules:

- Never mutate an `*Item` in place. `tx.get()` returns a clone. Change it, then
  `tx.put()` it. Snapshots in the undo stack and the board share pointers,
  so in-place edits would corrupt undo.
- Every change goes through a `Service` method: `begin` makes a transaction on a
  cloned board, then `commit` diffs the touched items, appends events (with
  fsync) and only then swaps the board. If the append fails, state is unchanged.
- Undo is in memory (depth from settings, default 10) and replays `Before`
  snapshots as a new, unrecorded transaction. Navigation (jumps) is never
  undoable. Link-title events are not undoable.
- Bump `store.FormatVersion` for any incompatible change to the event shape.
  Older binaries refuse newer files, and that behaviour is deliberate.
- The loader skips unparsable lines (a torn write after a crash). On open, the
  log terminates a partial last line before appending.
- New files start once the current one would pass `MaxSize` (10MB).
- The live data files are never truncated or rewritten. Only `backups/` is pruned.

**States.** `State` is `now | next | later | journal`. Completion is separate
(`Completed *time.Time`), so a done item keeps its state. `Item.Open()` means
"in an active view" (active state and not done). Journal items can't be
completed. Demoting to journal clears `Completed`. Moving a done item to an
active state reopens it.

**Hierarchy.** `Parent` is the tree. `Order` (float64) orders siblings: new
items get `MaxOrder()+1`, outdent uses a midpoint, and reorder swaps orders
among *view* siblings (`Board.viewSiblings`). A child whose parent isn't in
the same view shows at the top level with `Row.Context` (the parent title).
History applies the same rule per day group.

**Checkbox sync.** `Source` (the note whose body holds the line) is kept
separate from `Parent`, so re-parenting doesn't break the link. `Line` is
the checkbox ordinal at the last sync. Matching order: exact text, then near
match (normalised text or Levenshtein ≤ max(2, 20%)), then position. The body
owns the wording. The child owns done-ness, but a body edit that ticks or
unticks a line wins because it is the newest action. Every path that changes
a sourced child's title or done state must call `updateSourceLine`. Deleting
one calls `removeSourceLine`, which also shifts later ordinals. If a sync
removes or rewrites ≥ `BulkThreshold` children, the UI shows a warning with an
undo hint.

**Config files** are TOML, written atomically (temp file, fsync, rename).
Missing keys fall back to defaults and are normalised. If a file fails to parse,
the app runs on defaults, shows a warning, and **refuses to save over the
user's file** (`SettingsInvalid` / `KeysInvalid`).

**Key bindings** live in `config.Actions` (name, group, description,
defaults). Adding an action there is enough for `keys.toml`, the settings
pane and the help screen to pick it up. Groups are contexts: *Move* keys are
read only after `m`, and *Add/edit dialog* keys only inside the editor, so
they may overlap with list keys (`x` = done in lists, Next after `m`). Single
letter bindings must never fire while the editor has focus (the editor is
checked first in `handleKey`). Don't bind `ctrl+i`, because terminals send it
as Tab.

**Backups**: `backups/<2006-01-02T150405>/` copies of the data files, taken
with the log mutex held. One is made at launch if none exists from the last
24h. After that a one-minute ticker checks for the configured hour (default
11:00). Polling survives laptop sleep better than a single long timer.
Anything older than `backup_keep_days` (10) is pruned.

## UI conventions

- The screen has a header (tabs) on row 0, a blank row 1, content from row 2
  (`contentTop`), then a status line and a hints line. `contentHeight()` = height − 4.
- `refresh()` recomputes all four tabs' rows after any change and keeps
  each cursor on the same item ID. Call it after every successful mutation (the
  `run(err)` helper does).
- Mouse hit-testing uses the same layout helpers as rendering
  (`headerTabs`/`tabAt`, `contextSpan`, `buildLines`). If you change row layout,
  change them together and run `TestMouse`.
- Truncate plain text **before** styling (`seg` / `truncateSegs` /
  `renderRow`). Truncating styled strings can cut an OSC 8 hyperlink in half.
- Status messages: `info` (4s), `warn`/`fail` (10s). Use `fail` for errors. It
  shows refusals such as `ErrJournalDone` as warnings and real failures in red.
- Don't produce a status message *and* then overwrite it. Command
  constructors like `m.info()` change state as soon as they're called, so
  `someHelper(m.save(), m.info(...))` will always show the info. Use explicit
  `if cmd := …; cmd != nil { return cmd }`.
- Text selection: `v` releases the mouse (select mode). Rows have no left
  border, so selected text copies cleanly. `y` copies an item and its
  non-checkbox children as Markdown.

## Testing

- Table-driven tests with `assert`. Use `require` when later lines would panic.
- Domain: `internal/todo` uses a fake `memLog`, a fixed clock and sequential IDs
  (`newTestService`). `TestReplayMatchesLiveState` checks that the log replays to
  exactly the live board, so keep it passing whenever you add an operation.
- UI: `internal/ui/ui_test.go` has a `harness` that sends real
  `tea.KeyPressMsg` / mouse / paste messages (`keyMsg("alt+x")`,
  `h.typeText`, `h.add`) and asserts on `ansi.Strip(View().Content)` and
  model state. Prefer driving behaviour through keys over calling methods
  directly.
- IDs made in the same transaction share a timestamp, and their random suffix
  makes their order unpredictable. Don't pick items by `All()[0]` in tests.
  Look them up by title.
- To eyeball a screen, write a throwaway test that prints
  `ansi.Strip(h.m.View().Content)`, or drive the real binary in tmux:
  `tmux new -d -s tt -x 110 -y 30 './bin/todotil --home ./tmp/home'`, then
  `tmux send-keys` / `tmux capture-pane -p`. Use `send-keys -l -- "- [ ] …"`
  for text that starts with a dash.
- Never point manual runs at `~/.config/todotil`. Use `--home` or
  `TODOTIL_HOME`. The lock file stops two instances sharing a home.

## Style

- Match the surrounding code: small files per concern, doc comments on
  exported identifiers, and comments that explain *why*.
- Errors: wrap with context (`fmt.Errorf("%s: %w", path, err)`). Sentinel
  errors for refusals live in `todo/service.go`.
- Keep the domain free of UI concerns. `Result` / `SyncSummary` carry facts,
  and the UI turns them into words.

## Ideas not yet built

- Redo, and an undo stack that persists across restarts (the event log already
  has `tx` ids to support it).
- Nested checkbox lines becoming nested children. Today every checkbox child
  hangs directly off its note.
- Optional hidden anchors in note bodies (`<!-- id -->`), if position and
  fuzzy matching turn out to be too weak in practice.
- A search or filter over History.
