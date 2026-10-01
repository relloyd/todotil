package cli

// AgentGuide is printed by `todotil help agents`. It is written to be pasted
// into another repository's AGENTS.md.
const AgentGuide = `## Working from the todotil task list

The user tracks work in **todotil**, a local todo app with Now / Next / Later
lists. You can take items from it, report progress and mark them done with the
` + "`todotil`" + ` CLI. Always pass ` + "`--json`" + ` and your agent name, and check the exit code.

### Workflow

1. **Look.** Run ` + "`todotil list --json`" + ` to see Now (add ` + "`--list next`" + ` or
   ` + "`--list later`" + ` for the other lists). Each top-level item has a ` + "`position`" + `
   (1 = top) and an ` + "`id`" + `. Children are nested and have IDs but no position.
2. **Claim.** Run ` + "`todotil claim next --as <you> --json`" + ` to take the top item
   nobody holds. You can also claim ` + "`<position>`" + ` or ` + "`<item-id>`" + `.
   - **Save the ` + "`claim`" + ` ID from the response.** It stays valid wherever the
     user moves the item, and you need it for every later command.
   - The response includes the item's full body.
   - Claiming moves the item to Now.
   - Prefer ` + "`claim next`" + ` or an ID over a position, because the list can change
     between your ` + "`list`" + ` and your ` + "`claim`" + `.
3. **Read.** Run ` + "`todotil show <item-id|claim-id> --json`" + ` for the body, parent, children,
   notes and claim history.
4. **Report progress.** Run ` + "`todotil note <claim-id> \"text\" --json`" + ` at meaningful
   milestones. Notes are shown to the user and never change the item's text.
5. **Link pull requests.** As soon as you open a GitHub PR for the item, post its
   full URL as a note: ` + "`todotil note <claim-id> \"PR: https://github.com/<owner>/<repo>/pull/<n>\" --json`" + `.
   - Post one note per PR, including PRs in other repositories.
   - Notes stay on the item after it's done, so this is the user's record of
     where the work landed. Mention the PR again in your ` + "`done`" + ` note.
6. **Finish.** Run ` + "`todotil done <claim-id> [--note \"summary\"] --json`" + `.
   - If you can't finish, run ` + "`todotil release <claim-id> --reason \"why\" --json`" + `
     instead. The item stays put for someone else. Link any PR you opened first.
7. **Follow-ups.** Run ` + "`todotil add \"title\" --as <you> [--under <claim-id>] --json`" + `
   to record work you discovered.
   - The first line is the title. After a blank line comes the body, where
     ` + "`- [ ] step`" + ` lines become child todos.
   - Items go to Now unless you pass ` + "`--list next|later`" + `.
   - ` + "`--under`" + ` makes the new item a child of your claimed item.

### Exit codes

| Code | Meaning | What to do |
|---|---|---|
| 0 | OK. Check ` + "`already_done`" + ` / ` + "`already_held`" + ` in the JSON. | Continue. |
| 2 | Usage error or ambiguous ID prefix | Fix the command. |
| 3 | Someone else holds the claim | Pick another item. Pass ` + "`--steal`" + ` only if the user asked you to. |
| 4 | Not found: item deleted, position past the end, or nothing unclaimed | Re-list and choose again. |
| 5 | The item still has open children (listed in ` + "`error.details.children`" + `) | Finish or claim the children, or pass ` + "`--cascade`" + ` if they're genuinely done. |
| 6 | Your claim ended: released, rejected, stolen, unassigned by the user, or the item became a journal note | Stop work on it and tell the user. Don't re-claim without checking. |
| 7 | The item can't be claimed (journal note, already done, or rejected) | Pick another item. |
| 1 | Unexpected error | Report it. |

With ` + "`--json`" + `, failures also print ` + "`{\"error\": {\"code\", \"exit\", \"message\", \"details\"}}`" + ` to stdout.

### Rules

- **Identity.** Use one stable name per agent session (for example ` + "`claude-<task>`" + `).
  ` + "`$TODOTIL_AGENT`" + ` works in place of ` + "`--as`" + `.
- **One item at a time.** Claim one item, finish or release it, then take the next.
  Never leave a claim dangling: claims don't expire.
- **When you finish.** If ` + "`done`" + ` returns ` + "`already_done: true`" + `, the user
  completed the item. Treat it as finished.
- **The user.** The user can move, edit, unassign, complete or reject items at any time.
  Re-read the item with ` + "`show`" + ` before relying on details you fetched earlier.
- **Rejection.** Rejection is separate from completion. It ends an active claim
  with exit 6; ` + "`show`" + ` reports ` + "`rejected`" + `, ` + "`rejected_at`" + ` and ` + "`rejected_by`" + `.
- **IDs.** Item and claim IDs accept any unique prefix of 6+ characters. Positions
  are numbers with fewer than 6 digits.
`
