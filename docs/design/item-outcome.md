# Item outcome as a single field

Status: **proposed**, not started. Raised in the review of PR #2 (rejected
outcomes), deferred so that PR could ship.

## Problem

An item can be closed in two ways: completed or rejected. Each is stored as
its own pair of nullable fields on `todo.Item`:

```go
Completed   *time.Time `json:"completed,omitempty"`
CompletedBy string     `json:"completed_by,omitempty"`
RejectedAt  *time.Time `json:"rejected_at,omitempty"`
RejectedBy  string     `json:"rejected_by,omitempty"`
```

Only one may be set at a time, but the data model allows both. The rule is
kept by convention. If a write path forgets to clear the other pair, the item
becomes both `Done()` and `Rejected()`, and what happens next depends on
which check each caller makes first:

- `Open()` is false either way, so the item leaves the active views.
- The list marker, detail badge and CLI `state` check `Done()` first and
  show "done".
- `Board.HistoryRows` with the Completed filter and the Rejected filter
  both include it.
- `tx.complete` refuses with `ErrRejected`, and `tx.reject` refuses with
  `ErrCannotRejectDone`, so the user can't close it either way, only reopen it.

Before the review fixes, `setRejected` didn't clear `Completed`, and the
checkbox sync set the fields by hand in two places. No path was known to
produce such an item, since `reject` refuses done items first, but nothing
made it impossible.

A third outcome (for example "deferred" or "duplicate") would add a third
pair of fields and more rules to keep by hand.

## What is in place now

The review fixes put the rule in one place without changing the stored
format:

- **Writes** go through three methods in `internal/todo/item.go`:
  `markDone`, `markRejected` and `clearOutcome`. Each sets one pair and
  clears the other, and ends any active claim with the right `ClaimEnd`.
  `setDone`, `setRejected` and `syncBody` all use them.
- **Reads** can use `Item.Outcome() (Outcome, bool)`, which returns
  `{Kind, At, By}`. The History row, the detail view header and the CLI's
  `show` output are built from it or from the same pattern.

The four fields are still public and still written directly by
`Item.Clone`, `Item.Equal` and JSON decoding, so the compiler doesn't
enforce the rule.

## Proposal

Replace the four fields with one:

```go
type Outcome struct {
	Kind OutcomeKind `json:"kind"`          // "done" | "rejected"
	At   time.Time   `json:"at"`
	By   string      `json:"by,omitempty"`
}

type Item struct {
	// ...
	Outcome *Outcome `json:"outcome,omitempty"`
}
```

- `Done()` becomes `it.Outcome != nil && it.Outcome.Kind == OutcomeDone`,
  and `Rejected()` changes the same way.
- `markDone`, `markRejected` and `clearOutcome` each assign or clear the
  single pointer. An item can no longer be both.
- A new outcome kind means a new constant and its call sites, with no new
  fields and no new clearing rules.

## Format and compatibility

The event log stores full item snapshots, so this changes what's on disk.

- **Preferred: before v3 is released.** PR #2 introduced format v3 for the
  rejection fields. If this change lands before any v3 build is in use, v3
  can simply mean "has `outcome`". v2 snapshots (`completed`,
  `completed_by`) are read into `Outcome{Kind: done}` on replay. Nothing on
  disk has the v3 fields yet.
- **After v3 ships:** bump `store.FormatVersion` to 4. Replay reads both
  shapes: legacy `completed*` / `rejected*` fields fill `Outcome`, with
  completion first if both are set. The first append starts a v4 file, as
  AGENTS.md describes. Older binaries refuse v4 files, as intended.

The agent JSON (`internal/cli/item_json.go`) should keep `done`,
`completed`, `completed_by`, `rejected`, `rejected_at` and `rejected_by`, so
the change doesn't break agents. It can add an `outcome` object alongside
them.

## Work involved

- `todo/item.go`: the field, `Done`/`Rejected`/`Outcome`, `Clone`, `Equal`,
  and the three write methods.
- `todo/board.go` or `store`: replay mapping from legacy fields.
- `store/log.go`: format version, if v3 has shipped.
- `todo/history.go`: `keyOf` for the completed sort reads `Outcome.At`.
- UI and CLI: switch the remaining direct field reads (`it.Completed`,
  `it.RejectedAt`) to `Outcome()`. `grep -rn 'Completed\b\|RejectedAt'`
  finds them.
- Tests: `TestReplayMatchesLiveState`, plus a replay test with v2 and v3
  snapshots and one with both legacy pairs set.

## Open questions

- Should `By` for the TUI user stay `""`, as elsewhere, or be `"you"`?
  Probably keep `""` for consistency with `CreatedBy` and event `By`.
- Should a reopen keep the previous outcome as history, the way claims do?
  Not needed today, and it would make undo harder to reason about.
