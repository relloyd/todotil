# AGENTS.md

Guidance for anyone (human or AI agent) working on **todotil**, a Bubble Tea
terminal app for todos and meeting notes with a Now / Next / Later workflow.
It also has a CLI that lets AI agents claim, annotate and complete items while
the TUI is open. For how agents *use* that CLI (in other repos), see
[docs/agent-cli.md](docs/agent-cli.md) and `todotil help agents`. This file
is about developing todotil itself.

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
| Data lock (per write) | `github.com/gofrs/flock` |
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
main.go                   global flags, legacy-lock check, dispatch to a CLI command or the TUI
internal/todo             domain: Item, Board (state + view queries), Service (operations, undo),
                          claims.go (agent claims/notes), checkbox sync
internal/store            JSONL event log shared between processes (split files, per-write lock), backups/prune
internal/cli              agent/script subcommands, JSON output, exit codes, `help agents` guide
internal/config           config.toml, keys.toml (Actions + KeyMap), theme.toml (palettes)
internal/links            URL/Markdown-link detection, page-title fetcher
internal/ui               Bubble Tea model: model.go (dispatch), render.go, editor.go,
                          detail.go, settings.go (settings + help), actions.go, mouse.go, background.go
```

Keep the dependency direction: `ui, cli → todo, store, config, links`.
`store → todo` for the `Event` type only. `todo` imports nothing from this
repo.

## Architecture and invariants

**One record per item, event-sourced.** `data/events-NNNNNN.jsonl` is an
append-only log of `put` (full item snapshot), `del`, and `link` (cached page
title) events. Every file starts with a `meta` line holding the format
version. The board is rebuilt by replaying all files in name order, last
write wins. Rules:

- Never mutate an `*Item` in place. `tx.get()` returns a clone. Change it, then
  `tx.put()` it. Snapshots in the undo stack and the board share pointers,
  so in-place edits would corrupt undo.
- Every change goes through a `Service` method built on `s.run(label, record, fn)`:
  1. **Sync.** Replay events other processes wrote.
  2. **Compute.** `fn` stages changes on a cloned board (`t.w`).
  3. **Commit.** Diff the touched items. Then `Log.Update` takes the file lock,
     replays any events that arrived meanwhile, and checks that every touched
     item is still the **same pointer** it was. A put replaces the pointer, so
     identity means "unchanged".
  4. **Race.** If another process got in first, the commit returns `errRetry`
     and `run` recomputes `fn` on the new state (up to `maxRetries`).
     Selection logic like `claim next` therefore never hands two agents the
     same item. Put decisions inside `fn`, not before `run`.
  5. **Apply.** Only after a durable append are the mutations applied to the
     live board. If the append fails, state is unchanged.
- `fn` may run several times. Keep it free of side effects outside `t`, and reset
  any captured results at its start.
- Every event carries `By` (the actor: agent name, or `""` for the TUI user).
  `Board.LastActor` powers "claude-1 changed it since" messages.
- Undo is in memory (depth from settings, default 10) and only for the TUI user.
  It replays `Before` snapshots as a new, unrecorded transaction, **only if** every
  item still is the `After` pointer that action produced. Otherwise it returns
  `UndoConflictError` and the entry is dropped. Agent operations are never
  recorded (`record=false`). Navigation (jumps) and link titles aren't undoable.
- Bump `store.FormatVersion` whenever items gain fields an older binary would
  drop when it rewrites a snapshot. It is 3 now: rejection fields were added
  after v2 added claims, notes and `created_by`/`completed_by`.
  - Older binaries refuse newer files, and that behaviour is deliberate.
  - The first append after an upgrade starts a new file with the new header,
    because appending newer events under an older header would let old binaries
    misread them.
- **Many processes share the log.** There is no session lock.
  - `Log.Update` and `Log.Poll` hold `data/.lock` only while reading new bytes and
    appending. Each `Log` remembers how far it has read (file number and offset).
  - `Poll` checks sizes without the lock first, which makes it cheap enough for
    the TUI's 500ms tick.
  - Each process's `store.Log` holds its own flock descriptor. That makes
    goroutines with separate `Log`s a faithful stand-in for separate processes
    in tests.
- **Older binaries.** v1 binaries held `todotil.lock` for their whole session.
  `store.CheckLegacyLock` refuses to run while one does. Keep that check until
  v1 is long gone.
- The reader skips unparsable lines (a torn write after a crash). Before
  appending, a missing final newline is written first.
- New files start once the current one would pass `MaxSize` (10MB).
- The live data files are never truncated or rewritten. Only `backups/` is pruned.

**IDs.**
- New item IDs are 16 random hex characters.
- Items from v1 have timestamp-prefixed IDs, and items created in the same
  millisecond (checkbox children) share their first 11 characters.
  - So never abbreviate IDs to a fixed length. Use `Board.ShortIDs()`, which
    gives the shortest unique prefix of at least 8 characters.
  - `Board.Resolve` accepts any unique prefix of 6+ characters.
- Claim IDs are `c-` plus 12 hex characters. `Board.FindClaim` searches the
  claim history, so ended claims resolve too, which is how exit 6 is
  distinguished from exit 4.

**Claims** (`todo/claims.go`).
- `Item.Claims` is a history. Only the last entry can be active (`Ended == nil`).
- Every path that ends work must end the claim with the right `ClaimEnd`:
  - completion (`setDone`), including a ticked checkbox line: `done`
  - `Unassign`: `unassigned`
  - demotion to journal: `demoted`
  - `--steal`: `stolen`
  - `Release`: `released`
  - rejection: `rejected`
- `Finish` treats a claim that ended as `done` as success (`AlreadyDone`).
  Any other ending, including rejection, is `ClaimEndedError`.
- Notes are records on the item, never in the body. The body drives checkbox sync.

**States.** `State` is `now | next | later | journal`. Completion and rejection
are separate outcomes, so an item keeps its state. `Item.Open()` means "in an
active view" (active state and neither done nor rejected). Journal items can't
be completed or rejected. Demoting to journal clears terminal outcomes. Moving
a done or rejected item to an active state reopens it.

**Hierarchy.** `Parent` is the tree. `Order` (float64) orders siblings: new
items get `MaxOrder()+1`, outdent uses a midpoint, and reorder swaps orders
among *view* siblings (`Board.viewSiblings`). A child whose parent isn't in
the same view shows at the top level with `Row.Context` (the parent title).
History applies the same rule per day group. Its transient filter cycles
All, Completed, Rejected and Journal; outcome sorting uses the completion or
rejection timestamp.

**Checkbox sync.** `Source` (the note whose body holds the line) is kept
separate from `Parent`, so re-parenting doesn't break the link. `Line` is
the checkbox ordinal at the last sync. Matching order: exact text, then near
match (normalised text or Levenshtein ≤ max(2, 20%)), then position. The body
owns the wording. The child owns its outcome, which the line mirrors as
`[ ]` open, `[x]` done or `[-]` rejected. A body edit that changes a line's
mark wins because it is the newest action, so ticking a rejected child's line
completes it. Removing a completed or rejected checkbox child detaches it so
its history survives. Every path that changes a sourced child's title or
outcome must call `updateSourceLine`. Deleting one calls `removeSourceLine`,
which also shifts later ordinals. If a sync removes or rewrites ≥
`BulkThreshold` children, the UI shows a warning with an undo hint.

**Outcomes.** Completion and rejection are separate fields that must never
both be set. Write them only through `Item.markDone`, `markRejected` and
`clearOutcome`, and read them through `Done()`, `Rejected()` or `Outcome()`.
[docs/design/item-outcome.md](docs/design/item-outcome.md) describes the
planned single-field model.

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
while holding the data lock. Only the TUI makes backups; CLI commands don't. One is made at launch if none exists from the last
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
  `tea.KeyPressMsg` / mouse / paste messages (`keyMsg("ctrl+x")`,
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
  `TODOTIL_HOME`.
- Multi-process behaviour:
  - `todo` tests use `memLog`. Its `pending` field holds events "from another
    process", and `race` injects a write between compute and commit.
  - `ui` tests use `newSharedHarness`, which puts an agent `Service` on the same
    on-disk log. Send `syncTickMsg{}` to make the TUI poll.
  - `cli` tests run `Run()` in goroutines. Each call opens its own `Log`, so
    they contend for the flock like separate processes
    (`TestConcurrentClaimNext`).
- End-to-end: start the TUI in tmux on a scratch home, run `bin/todotil --home
  … claim/done …` in the shell, then `capture-pane`. That's how the live-reload,
  unassign and already-done paths were verified.

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
- Claim leases/heartbeats, if abandoned claims become a nuisance. Today they
  never expire, and the TUI shows their age.
- A `todotil watch` or `--wait` for agents that want to block until work appears.
- Filtering `list` by assignee or creator.
- Nested checkbox lines becoming nested children. Today every checkbox child
  hangs directly off its note.
- Optional hidden anchors in note bodies (`<!-- id -->`), if position and
  fuzzy matching turn out to be too weak in practice.
- A search over History.
