# Agent CLI

AI agents (and scripts) use `todotil` subcommands to pick up work, report
progress and mark it done. They share the data with a running TUI.

## Commands

The assignee comes from `--as NAME`, or `$TODOTIL_AGENT` when `--as` isn't given. All
commands accept `--json`: agents should always pass it. With `--json`, errors
are printed to stdout as `{"error": {...}}` as well as setting the exit code.

```
todotil list    [--list now|next|later] [--json]
todotil show    <item-id|claim-id> [--json]
todotil claim   <position|item-id|next> --as NAME [--list L] [--steal] [--json]
todotil done    <claim-id> [--cascade] [--note TEXT] [--json]
todotil release <claim-id> [--reason TEXT] [--json]
todotil note    <claim-id> TEXT [--json]
todotil add     TEXT --as NAME [--parent ITEM-ID | --under CLAIM-ID] [--list L] [--json]
todotil help agents          # instructions to paste into other repos' AGENTS.md
```

- **IDs.** Item IDs are immutable. Moving, reordering or indenting an item
  never changes its ID. A claim has its own immutable ID (`c-…`). Any unique
  prefix of at least 6 characters is accepted.
  - New item IDs are random hex, so short prefixes work well.
  - Items created before the agent CLI have IDs that start with a timestamp.
    Items created together share a long prefix.
  - Text output therefore shows each ID at its shortest unique length (at
    least 8 characters), like git.
- **Positions** are 1-based and count the **top-level** rows of a list (Now by
  default), which are the rows between the separator lines in the TUI. Children
  aren't counted; claim a child by its ID. An argument of fewer than 6 digits
  is a position. Anything else is treated as an ID.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | OK. This includes `done` on an item a human already completed (`already_done: true`) and re-claiming your own claim (`already_held: true`). |
| 1 | I/O or unexpected error |
| 2 | Usage error: bad arguments, missing `--as`, or an ambiguous ID prefix |
| 3 | Already claimed by someone else. The holder and claim time are in the error details. Use `--steal` to take it over. |
| 4 | Not found: no such item or claim, the item was deleted, the position is past the end, or `claim next` found nothing unclaimed |
| 5 | The parent still has open children. They're listed in the error details. Use `--cascade` to complete them too. |
| 6 | The claim is no longer valid (released, rejected, stolen, unassigned by the user, or the item was demoted to a journal note) |
| 7 | The item can't be claimed because it's a journal note, already done, or rejected |

## Rules

**Claim**
- Claiming an item that isn't in Now moves it there, as `m n` does in the
  TUI: open children in the same state follow.
- If you claim an item you already hold, you get your existing claim back.
- Claiming a parent doesn't claim its children.
- `claim next` picks the top-level item nearest the top that nobody holds.
  The pick and the claim happen under one lock, so two agents never get the same item.
- `--steal` ends the current holder's claim. Their claim ID then fails with exit 6.

**Done**
- `done` works wherever the item has been moved since it was claimed.
- It completes the item and records `completed_by`.
- If the item is a checkbox child, its line in the parent note is ticked.
- If the user already completed the item, `done` succeeds with
  `already_done: true` and reports who completed it.
- If the item has open children, `done` fails with exit 5 unless you pass `--cascade`.
- Rejection is a separate outcome, not completion. It ends an active claim with
  exit 6; `show` reports `rejected`, `rejected_at` and `rejected_by`.

**Release** gives the item up. It stays where it is and becomes claimable again.

**Notes** are timestamped records on the item. They are not part of its body.
The detail view shows them, and so do `show` and copy.

**Add**
- `add` creates an item in Now unless you pass `--list`. The one exception:
  under a parent that is open in an active list, it defaults to that list.
- `--list journal` records a journal note.
- The text can be `-` to read it from stdin.
- `--parent` or `--under` makes it a child. `--under` takes a claim ID and uses
  the claimed item as the parent.
- It records `created_by`. Checkbox lines in the text become child todos, as
  they do in the TUI.

**Claims never expire.** They end on done or rejection, on release, when stolen,
when the user unassigns them (`U` in the TUI), or when the item is demoted to a journal note.

## Sharing data with the TUI

- **Locking**
  - There is no session lock.
  - Each process takes `data/.lock` only while it replays new events and
    appends its own.
  - Every write is checked against the latest state, including changes
    made by other processes.
  - If two writers race, the loser retries.
- **Live reload.** The TUI checks the log every 500ms and reloads when it
  grows. It shows assignee badges (`@claude-1 2h`), `done by …` and rejection
  outcomes in History, and a `✦` marker on items that agents created.
- **Undo** in the TUI only applies if every item it touches is still exactly
  as your action left it. Otherwise it's refused and dropped. Agent changes
  never go on the TUI's undo stack.
- **Editing.** If an item's title or body changed while you had its edit dialog
  open, saving asks you to press save again to overwrite.
- **Format version 3.** The log format is version 3 because items gained
  rejection outcomes (`rejected_at`, `rejected_by`).
  - An older binary refuses version 3 files rather than silently dropping those fields.
  - The first write by a new binary starts a fresh file with a version 3 header.
- **Older binaries.** A new binary refuses to run while an older one holds the old
  session lock.
