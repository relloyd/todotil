# Syncing through a shared S3 bucket

Status: **proposed**, not started.

Today every todotil process on one machine shares the event log in
`<home>/data`, and `flock` keeps their writes in order. This note describes
how the same log could live in an S3 bucket, so the TUI and agents on
different machines stay in sync. The goal is to keep the domain
(`internal/todo`) unchanged.

## What must carry over

The event log's rules (see AGENTS.md, "Architecture and invariants") give
two guarantees that any shared store has to keep:

1. **One total order.** Every client replays the same event sequence, and
   later writes win. Boards built from the same events are identical
   (`TestReplayMatchesLiveState`).
2. **Compare-and-swap at commit.** `Service.run` computes a change on a
   cloned board, then calls `Log.Update`. Under the lock, `Update` passes
   the other processes' new events to the `apply` function. `apply` replays
   them and checks that every touched item is still the same pointer. If
   anything changed, it returns `errRetry` and nothing is written. This is
   why `claim next` never gives two agents the same item.

S3 has no append and no locks. Since August 2024, though, it supports
**conditional writes**. `PUT` with `If-None-Match: *` creates an object only
if the key doesn't exist and otherwise fails with `412 Precondition Failed`.
(`If-Match` on an ETag followed in November 2024.)
That is enough to build compare-and-swap without a lock.

## Layout: one object per transaction

```
s3://<bucket>/<prefix>/
  format.json                         {"v": 2}     (see "Format version")
  log/000000000001.jsonl              events of one transaction, immutable
  log/000000000002.jsonl
  ...
  snapshots/000000000500.jsonl.gz     full board as of transaction 500
```

- Each `log/` object holds the events of one `Service.run` commit, in the
  same JSONL encoding as today's files (`put`, `del`, `link`). The key is a
  zero-padded sequence number, so names sort in replay order.
- Objects are never changed after they're written. The sequence number is the
  only coordination point.
- Readers stop at the first missing number. Writers only ever create the
  next number after the highest one they have seen, so the log has no gaps.

## Write path

A new `store.S3Log` implements the existing `todo.Log` interface:

```go
type Log interface {
	Update(fn func(foreign []Event) ([]Event, error)) error
	Poll() ([]Event, error)
}
```

`Update(fn)`:

1. Fetch `log/{n+1}`, `log/{n+2}`, … until a 404, where `n` is the last
   sequence number this client has read. These are the foreign events.
2. `out, err := fn(foreign)`. On `errRetry` or an empty `out`, return as
   today.
3. `PUT log/{n+1}` with body `out` and `If-None-Match: *`.
   - **200:** set `n = n+1` and return.
   - **412:** another client took `n+1`. Go back to step 1. `fn` gets only
     the newly fetched events, replays them and repeats its check. If none
     of our touched items changed, it returns the same `out` and we try
     `n+2`. Otherwise it returns `errRetry` and `Service.run` recomputes
     (up to `maxRetries`, as today).

This is the same shape as the file backend, with "take lock, read new bytes,
append" replaced by "read new objects, conditional create". `Service`, undo,
claims and checkbox sync need no changes.

A crash can't leave a partial transaction. A PUT either creates the whole
object or nothing, which also removes the torn-line handling the file
reader needs.

## Read path and polling

- **Poll** (the TUI's 500ms tick, possibly slower when idle): `GET
  log/{n+1}`. A 404 means nothing new. The request is as cheap as today's
  size check, only remote.
- **Catch-up** after sleep or time offline: `LIST log/` with
  `start-after=log/{n}`, then fetch the objects it names.
- S3 reads and lists have been strongly consistent since December 2020. An
  object is visible to every client as soon as its PUT returns.
- **Push, optional and later:** S3 event notifications to SNS or SQS (or
  EventBridge) could wake clients, with polling kept as the fallback.

## Snapshots, startup and retention

Replaying thousands of small objects at startup would be slow, so clients
write checkpoints:

- Once the tail passes a threshold (for example 500 transactions since the
  last snapshot), any client may write `snapshots/{seq}.jsonl.gz`: one `put`
  per item, plus link titles, as of `seq`.
- The snapshot at a given `seq` is deterministic, so clients can't race
  harmfully. Use `If-None-Match: *` and ignore a 412.
- **Startup:** `LIST snapshots/`, load the newest, then fetch `log/` objects
  after its sequence number.
- **Retention:** keep old `log/` objects (they are the full history) and let
  a lifecycle rule move them to cheaper storage. Turn on bucket versioning.
  Together these replace the local `backups/` directory for S3-backed homes.

## Local cache and offline use

- Keep a local mirror of fetched objects and snapshots under `<home>/data`,
  so startup reads mostly from disk and the TUI still works for reading
  when offline.
- **Writes while offline** are the hard part. `fn` is a closure over the
  current board. It can't be queued and re-run later against a board it
  never saw.
  - **First version:** require a connection to write. The TUI shows
    "offline, read-only". The CLI fails with a new exit code, so agents back
    off.
  - **Later, if needed:** record each change with the version of every item
    it was based on. On reconnect, commit it only if those items are
    unchanged, and otherwise report the conflict. This is a separate
    project.

## Several processes on one machine

Each process (the TUI, each agent's CLI call) can talk to S3 directly. The
conditional PUT orders them as `flock` does today. If they share the local
cache, `flock` still guards writes to the mirror, but only as a cache and
not for correctness.

## Format version

- `format.json` holds the version that today sits in each file's `meta`
  line. It is written once, with `If-None-Match: *`.
- A client refuses to read if the version is newer than it understands, and
  refuses to write if it's older. That keeps today's rule that older
  binaries refuse newer data.
- Raising the version means conditionally overwriting `format.json`
  (`If-Match: <etag>`). Clients that are still running then see the new
  version on their next poll and stop writing.
- A per-object `meta` line is unnecessary, because every object in one
  prefix shares the bucket's version.

## Configuration

```toml
# config.toml
[storage]
url = "s3://my-bucket/todotil"   # empty = local files, as today
region = "eu-west-2"
```

Credentials come from the standard AWS chain (environment, profile, SSO,
instance role). The TUI and agents on one machine use the same settings.
Agents on other machines need only the URL and credentials.

## Costs

These are rough figures at AWS S3 Standard list prices; check current pricing.

- Polling every 2s is about 43,000 GETs a day, roughly $0.02 per client per
  day. Polling more slowly when idle cuts this further.
- Writes are one PUT per user action or agent call, a negligible cost.
- Storage is tiny. Objects are a few KB, plus snapshots.

## Security

- The bucket should block public access, use SSE-KMS, and have versioning
  enabled.
- Give each machine or agent its own IAM role, limited to the prefix.
  CloudTrail data events then record who wrote each object, which could be
  checked against the event's `By` field.

## Latency

A commit becomes one or two network round trips (about 30–100ms in-region)
instead of a local append. That's acceptable for a todo app. A 412 retry
costs one extra round trip and only happens when two writes collide.

## Alternatives considered

- **One log per client, merged by timestamp.** Each client writes only to
  its own prefix, so writes never contend. But nothing orders writes
  globally, so two agents could both claim the same item, and clock skew
  decides which edit wins. Rejected: the claim guarantee matters more than
  avoiding the occasional 412.
- **A lock object** (conditional create of `lock`, delete to release).
  Rejected: a crashed client leaves a stale lock that needs leases and
  timeouts. The sequence-number approach needs no lock.
- **Syncing the files with Dropbox or iCloud.** Rejected: file-sync tools
  don't honour `flock` across machines and can produce conflicted copies of
  a log file.
- **A small server (HTTP or SQLite) in front.** This would work, but means
  running and hosting a service. S3 gives the ordering guarantee with
  nothing to operate.

## Other providers

Conditional create is the one hard requirement. AWS S3 supports it
(`If-None-Match: *`), and Google Cloud Storage supports it
(`ifGenerationMatch=0`). Verify MinIO, Cloudflare R2 and other
S3-compatible stores before relying on them, and fail fast at startup if a
test conditional PUT isn't honoured.

## Work involved

1. `store.S3Log` implementing `todo.Log`, using the AWS SDK for Go v2.
2. A `store.Open` factory that picks the backend from `[storage] url`.
3. Snapshot writing and loading, and startup from a snapshot.
4. Offline detection and read-only mode in the TUI, and a CLI exit code.
5. Tests: run the existing `Service` and CLI concurrency tests
   (`TestConcurrentClaimNext`) against MinIO in a container. Add a test
   that injects a 412 between fetch and PUT.
6. A `todotil migrate --to s3://…` command to upload an existing local log
   as one snapshot plus an empty tail.

## Open questions

- Should the poll interval back off when idle, or should push notifications
  be the default when configured?
- Should snapshots be compacted into the local mirror, so the local files
  stop growing?
- Is a shared bucket per team in scope? That would need per-user views or
  filtering, which todotil doesn't have today.
