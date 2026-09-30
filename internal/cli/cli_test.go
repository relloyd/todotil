package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/relloyd/todotil/internal/config"
)

type result struct {
	code   int
	stdout string
	stderr string
}

// json decodes stdout.
func (r result) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &m), r.stdout)
	return m
}

type fixture struct {
	t     *testing.T
	paths config.Paths
	env   map[string]string
}

func newFixture(t *testing.T) *fixture {
	return &fixture{t: t, paths: config.Paths{Home: t.TempDir()}, env: map[string]string{}}
}

func (f *fixture) run(args ...string) result {
	f.t.Helper()
	var out, errOut bytes.Buffer
	code := Run(Env{
		Paths:  f.paths,
		Stdin:  strings.NewReader("from stdin\n\n- [ ] step"),
		Stdout: &out,
		Stderr: &errOut,
		Getenv: func(k string) string { return f.env[k] },
	}, args)
	return result{code, out.String(), errOut.String()}
}

// ok runs a command that must succeed with --json and returns its output.
func (f *fixture) ok(args ...string) map[string]any {
	f.t.Helper()
	r := f.run(append(args, "--json")...)
	require.Equal(f.t, ExitOK, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	return r.json(f.t)
}

// fails runs a command that must fail with code and returns the error object.
func (f *fixture) fails(code int, args ...string) map[string]any {
	f.t.Helper()
	r := f.run(append(args, "--json")...)
	require.Equal(f.t, code, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	return r.json(f.t)["error"].(map[string]any)
}

func (f *fixture) addItems(titles ...string) []string {
	var ids []string
	for _, title := range titles {
		out := f.ok("add", title, "--as", "setup")
		ids = append(ids, out["item"].(map[string]any)["id"].(string))
	}
	return ids
}

func listTitles(out map[string]any) []string {
	var titles []string
	for _, it := range out["items"].([]any) {
		titles = append(titles, it.(map[string]any)["title"].(string))
	}
	return titles
}

func TestUsageErrors(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name string
		args []string
	}{
		{"no command", nil},
		{"unknown command", []string{"frobnicate"}},
		{"claim without assignee", []string{"claim", "1"}},
		{"claim with no target", []string{"claim", "--as", "a"}},
		{"bad list", []string{"list", "--list", "journal"}},
		{"bad flag", []string{"done", "c-1", "--nope"}},
		{"position zero", []string{"claim", "0", "--as", "a"}},
		{"add without text", []string{"add", "--as", "a"}},
		{"add with both parents", []string{"add", "x", "--as", "a", "--parent", "p", "--under", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := f.run(tt.args...)
			assert.Equal(t, ExitUsage, r.code, r.stderr)
			assert.Contains(t, r.stderr, "todotil:")
		})
	}
}

func TestListShowsPositionsAndNesting(t *testing.T) {
	f := newFixture(t)
	f.addItems("first", "second\n\n- [ ] sub one\n- [ ] sub two")
	f.ok("add", "later thing", "--as", "setup", "--list", "later")

	out := f.ok("list")
	assert.Equal(t, "now", out["list"])
	items := out["items"].([]any)
	require.Len(t, items, 2)
	second := items[1].(map[string]any)
	assert.EqualValues(t, 2, second["position"])
	assert.EqualValues(t, 2, second["open_children"])
	kids := second["children"].([]any)
	require.Len(t, kids, 2)
	assert.Equal(t, "sub one", kids[0].(map[string]any)["title"])
	_, hasPos := kids[0].(map[string]any)["position"]
	assert.False(t, hasPos, "children have no position")

	assert.Equal(t, []string{"later thing"}, listTitles(f.ok("list", "--list", "later")))

	r := f.run("list")
	require.Equal(t, ExitOK, r.code)
	assert.Contains(t, r.stdout, "Now · 2 items")
	assert.Contains(t, r.stdout, "  2  ")
	assert.Contains(t, r.stdout, "└ sub one")
}

func TestClaimWorkflow(t *testing.T) {
	f := newFixture(t)
	ids := f.addItems("alpha", "beta")
	f.ok("add", "gamma", "--as", "setup", "--list", "next")

	// Claim by position; the claim ID is distinct from the item ID.
	out := f.ok("claim", "2", "--as", "bot-1")
	claim := out["claim"].(string)
	assert.True(t, strings.HasPrefix(claim, "c-"))
	assert.Equal(t, ids[1], out["item"].(map[string]any)["id"])
	assert.Equal(t, false, out["already_held"])

	// Claiming from Next moves the item to Now.
	out = f.ok("claim", "1", "--list", "next", "--as", "bot-2")
	assert.Equal(t, "next", out["moved_from"])
	assert.Contains(t, listTitles(f.ok("list")), "gamma")

	// Conflict, then steal.
	e := f.fails(ExitClaimed, "claim", ids[1], "--as", "bot-3")
	assert.Equal(t, "claimed", e["code"])
	assert.Equal(t, "bot-1", e["details"].(map[string]any)["assignee"])
	stolen := f.ok("claim", ids[1][:8], "--as", "bot-3", "--steal")
	assert.Equal(t, "bot-1", stolen["stole_from"])

	// The original claim is now stale.
	e = f.fails(ExitClaimEnded, "done", claim)
	assert.Equal(t, "stolen", e["details"].(map[string]any)["end"])

	// Notes and done on the new claim.
	newClaim := stolen["claim"].(string)
	f.ok("note", newClaim, "halfway", "there")
	done := f.ok("done", newClaim, "--note", "shipped")
	assert.Equal(t, false, done["already_done"])
	assert.Equal(t, "bot-3", done["completed_by"])

	show := f.ok("show", newClaim)
	notes := show["notes"].([]any)
	require.Len(t, notes, 2)
	assert.Equal(t, "halfway there", notes[0].(map[string]any)["text"])
	assert.Equal(t, true, show["done"])
	claims := show["claims"].([]any)
	require.Len(t, claims, 2)
	assert.Equal(t, "stolen", claims[0].(map[string]any)["end"])
	assert.Equal(t, "done", claims[1].(map[string]any)["end"])

	// Done again is idempotent.
	assert.Equal(t, true, f.ok("done", newClaim)["already_done"])
}

func TestClaimNextAndRelease(t *testing.T) {
	f := newFixture(t)
	f.addItems("one", "two")
	f.env[AgentEnv] = "env-bot"
	first := f.ok("claim", "next")
	assert.Equal(t, "env-bot", first["assignee"])
	assert.Equal(t, "one", first["item"].(map[string]any)["title"])
	second := f.ok("claim", "next")
	assert.Equal(t, "two", second["item"].(map[string]any)["title"])
	f.fails(ExitNotFound, "claim", "next")

	f.ok("release", first["claim"].(string), "--reason", "blocked")
	again := f.ok("claim", "next", "--as", "other")
	assert.Equal(t, "one", again["item"].(map[string]any)["title"])
	f.fails(ExitClaimEnded, "note", first["claim"].(string), "still here?")
}

func TestDoneWithOpenChildren(t *testing.T) {
	f := newFixture(t)
	f.addItems("parent\n\n- [ ] kid a\n- [ ] kid b")
	c := f.ok("claim", "1", "--as", "bot")["claim"].(string)
	e := f.fails(ExitOpenChildren, "done", c)
	kids := e["details"].(map[string]any)["children"].([]any)
	assert.Len(t, kids, 2)

	r := f.run("done", c)
	assert.Contains(t, r.stderr, "pass --cascade")

	out := f.ok("done", c, "--cascade")
	assert.EqualValues(t, 2, out["cascaded"])
	assert.Empty(t, f.ok("list")["items"])
}

func TestNotClaimableAndNotFound(t *testing.T) {
	f := newFixture(t)
	j := f.ok("add", "standup notes", "--as", "setup", "--list", "journal")["item"].(map[string]any)["id"].(string)
	f.fails(ExitNotClaimable, "claim", j, "--as", "bot")
	f.fails(ExitNotFound, "claim", "5", "--as", "bot")
	f.fails(ExitNotFound, "claim", "abcdef123", "--as", "bot")
	f.fails(ExitNotFound, "done", "c-000000000000")
	f.fails(ExitNotFound, "show", "zzzzzzzz")
}

func TestAddVariants(t *testing.T) {
	f := newFixture(t)
	ids := f.addItems("parent")
	c := f.ok("claim", "1", "--as", "bot")["claim"].(string)

	under := f.ok("add", "found a bug", "--as", "bot", "--under", c)["item"].(map[string]any)
	assert.Equal(t, ids[0], under["parent"].(map[string]any)["id"])
	assert.Equal(t, "bot", under["created_by"])
	assert.Equal(t, "now", under["state"], "follows the open parent")

	child := f.ok("add", "later child", "--as", "bot", "--parent", ids[0][:8], "--list", "later")["item"].(map[string]any)
	assert.Equal(t, "later", child["state"])

	stdin := f.ok("add", "-", "--as", "bot")["item"].(map[string]any)
	assert.Equal(t, "from stdin", stdin["title"])
	assert.Len(t, stdin["children"], 1)

	// Flags after "--" are text.
	r := f.run("add", "--as", "bot", "--json", "--", "--not-a-flag", "--json")
	require.Equal(t, ExitOK, r.code, r.stderr)
	assert.Equal(t, "--not-a-flag --json", r.json(t)["item"].(map[string]any)["title"])
}

func TestTextOutput(t *testing.T) {
	f := newFixture(t)
	f.addItems("write docs\n\nexplain the claim flow")
	r := f.run("claim", "1", "--as", "bot")
	require.Equal(t, ExitOK, r.code, r.stderr)
	assert.Contains(t, r.stdout, `Claimed "write docs" as bot`)
	assert.Contains(t, r.stdout, "claim: c-")
	assert.Contains(t, r.stdout, "explain the claim flow")

	r = f.run("claim", "1", "--as", "someone")
	assert.Equal(t, ExitClaimed, r.code)
	assert.Contains(t, r.stderr, "is claimed by bot")
	assert.Empty(t, r.stdout, "no JSON without --json")
}

func TestHelp(t *testing.T) {
	f := newFixture(t)
	r := f.run("help")
	assert.Contains(t, r.stdout, "todotil claim <position|item-id|next>")
	r = f.run("help", "agents")
	assert.Contains(t, r.stdout, "## Working from the todotil task list")
	assert.Contains(t, r.stdout, "claim next --as <you> --json")
}

// Several agents racing for work each get a different item, and every
// item is taken exactly once.
func TestConcurrentClaimNext(t *testing.T) {
	f := newFixture(t)
	const items, agents = 12, 6
	var titles []string
	for i := range items {
		titles = append(titles, fmt.Sprintf("task %02d", i))
	}
	f.addItems(titles...)

	var (
		mu  sync.Mutex
		got = map[string]string{}
		wg  sync.WaitGroup
	)
	for a := range agents {
		wg.Go(func() {
			name := fmt.Sprintf("agent-%d", a)
			for {
				r := f.run("claim", "next", "--as", name, "--json")
				if r.code == ExitNotFound {
					return
				}
				if !assert.Equal(t, ExitOK, r.code, r.stderr) {
					return
				}
				title := r.json(t)["item"].(map[string]any)["title"].(string)
				mu.Lock()
				prev, dup := got[title]
				got[title] = name
				mu.Unlock()
				assert.False(t, dup, "%s claimed by %s and %s", title, prev, name)
			}
		})
	}
	wg.Wait()
	assert.Len(t, got, items)
}
