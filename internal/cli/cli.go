// Package cli implements the todotil subcommands that agents and scripts
// use alongside the TUI.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/relloyd/todotil/internal/config"
	"github.com/relloyd/todotil/internal/store"
	"github.com/relloyd/todotil/internal/todo"
)

// Exit codes. They are part of the documented interface for agents.
const (
	ExitOK           = 0
	ExitError        = 1
	ExitUsage        = 2
	ExitClaimed      = 3
	ExitNotFound     = 4
	ExitOpenChildren = 5
	ExitClaimEnded   = 6
	ExitNotClaimable = 7
)

// AgentEnv is the environment variable read when --as is not given.
const AgentEnv = "TODOTIL_AGENT"

// Env is what a command needs from the outside world.
type Env struct {
	Paths  config.Paths
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// Now overrides the clock in tests.
	Now func() time.Time
}

type command struct {
	name    string
	summary string
	run     func(c *ctx, args []string) error
}

var commands []command

func init() {
	commands = []command{
		{"list", "list [--list now|next|later] [--json]", runList},
		{"show", "show <item-id|claim-id> [--json]", runShow},
		{"claim", "claim <position|item-id|next> --as NAME [--list L] [--steal] [--json]", runClaim},
		{"done", "done <claim-id> [--cascade] [--note TEXT] [--json]", runDone},
		{"release", "release <claim-id> [--reason TEXT] [--json]", runRelease},
		{"note", "note <claim-id> TEXT [--json]", runNote},
		{"add", "add TEXT --as NAME [--parent ITEM-ID | --under CLAIM-ID] [--list L] [--json]", runAdd},
		{"help", "help [agents]", runHelp},
	}
}

// IsCommand reports whether name is a subcommand.
func IsCommand(name string) bool {
	return slices.ContainsFunc(commands, func(c command) bool { return c.name == name })
}

// ctx carries per-invocation state.
type ctx struct {
	env    Env
	json   bool
	svc    *todo.Service
	shorts map[string]string
}

// usageError marks bad arguments (exit 2).
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

// Run executes args (without the program name) and returns the exit code.
func Run(env Env, args []string) int {
	c := &ctx{env: env}
	flagArgs := args
	if i := slices.Index(args, "--"); i >= 0 {
		flagArgs = args[:i]
	}
	c.json = slices.Contains(flagArgs, "--json") || slices.Contains(flagArgs, "-json")
	if len(args) == 0 {
		return c.report(usagef("missing command"))
	}
	i := slices.IndexFunc(commands, func(cmd command) bool { return cmd.name == args[0] })
	if i < 0 {
		return c.report(usagef("unknown command %q", args[0]))
	}
	return c.report(commands[i].run(c, args[1:]))
}

// open loads the shared data and returns a service acting as actor.
func (c *ctx) open(actor string) error {
	log, loaded, err := store.Open(c.env.Paths.Data())
	if err != nil {
		return err
	}
	b := todo.NewBoard()
	for _, e := range loaded.Events {
		b.Replay(e)
	}
	c.svc = todo.NewService(b, log, 0)
	c.svc.Actor = actor
	if c.env.Now != nil {
		c.svc.Now = c.env.Now
	}
	return nil
}

func (c *ctx) now() time.Time {
	if c.env.Now != nil {
		return c.env.Now()
	}
	return time.Now()
}

// flags is a flag set that allows flags after positional arguments and
// treats everything after "--" as positional.
type flags struct {
	*flag.FlagSet
	asName *string
	json   *bool
}

func newFlags(name string, withAs bool) *flags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := &flags{FlagSet: fs, json: fs.Bool("json", false, "print JSON")}
	if withAs {
		f.asName = fs.String("as", "", "assignee name (default $"+AgentEnv+")")
	}
	return f
}

func (f *flags) parse(args []string) ([]string, error) {
	var rest []string
	if i := slices.Index(args, "--"); i >= 0 {
		args, rest = args[:i], args[i+1:]
	}
	var pos []string
	for {
		if err := f.Parse(args); err != nil {
			return nil, usagef("%s: %v", f.Name(), err)
		}
		args = f.Args()
		if len(args) == 0 {
			return append(pos, rest...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func (c *ctx) actor(f *flags) (string, error) {
	name := strings.TrimSpace(*f.asName)
	if name == "" && c.env.Getenv != nil {
		name = strings.TrimSpace(c.env.Getenv(AgentEnv))
	}
	if name == "" {
		return "", usagef("%s: an assignee is required: pass --as NAME or set $%s", f.Name(), AgentEnv)
	}
	return name, nil
}

func parseList(s string, allowJournal bool) (todo.State, error) {
	st, err := todo.ParseState(s)
	if err != nil || (st == todo.Journal && !allowJournal) {
		return "", usagef("--list must be now, next or later, got %q", s)
	}
	return st, nil
}

// print writes v as JSON, or text via the given function.
func (c *ctx) print(v any, text func(w io.Writer)) error {
	if c.json {
		enc := json.NewEncoder(c.env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	text(c.env.Stdout)
	return nil
}

// errorJSON is printed on stdout for failures in --json mode.
type errorJSON struct {
	Error struct {
		Code    string `json:"code"`
		Exit    int    `json:"exit"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	} `json:"error"`
}

// classify maps an error to its exit code, a stable code name and details.
func classify(err error) (int, string, any) {
	var (
		ue    usageError
		ce    *todo.ClaimedError
		nc    *todo.NeedsConfirmError
		ended *todo.ClaimEndedError
	)
	switch {
	case errors.As(err, &ue), errors.Is(err, todo.ErrNoAssignee), errors.Is(err, todo.ErrEmpty):
		return ExitUsage, "usage", nil
	case errors.Is(err, todo.ErrAmbiguous):
		return ExitUsage, "ambiguous_id", nil
	case errors.As(err, &ce):
		return ExitClaimed, "claimed", map[string]any{
			"item": ref(ce.Item), "assignee": ce.Claim.Assignee, "claim_id": ce.Claim.ID, "claimed_at": ce.Claim.At,
		}
	case errors.Is(err, todo.ErrNotFound):
		return ExitNotFound, "not_found", nil
	case errors.As(err, &nc):
		kids := make([]refJSON, len(nc.Children))
		for i, k := range nc.Children {
			kids[i] = ref(k)
		}
		return ExitOpenChildren, "open_children", map[string]any{"children": kids}
	case errors.As(err, &ended):
		return ExitClaimEnded, "claim_ended", map[string]any{
			"item": ref(ended.Item), "claim_id": ended.Claim.ID, "end": ended.Claim.End,
			"ended_at": ended.Claim.Ended, "reason": ended.Claim.Reason,
		}
	case errors.Is(err, todo.ErrNotClaimable), errors.Is(err, todo.ErrJournalDone):
		return ExitNotClaimable, "not_claimable", nil
	}
	return ExitError, "error", nil
}

func (c *ctx) report(err error) int {
	if err == nil {
		return ExitOK
	}
	code, name, details := classify(err)
	msg := err.Error()
	if c.json {
		var out errorJSON
		out.Error.Code, out.Error.Exit, out.Error.Message, out.Error.Details = name, code, msg, details
		enc := json.NewEncoder(c.env.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	}
	fmt.Fprintln(c.env.Stderr, "todotil:", msg)
	var nc *todo.NeedsConfirmError
	if errors.As(err, &nc) {
		for _, k := range nc.Children {
			fmt.Fprintf(c.env.Stderr, "  %s  %s\n", c.shortID(k.ID), k.Title)
		}
		fmt.Fprintln(c.env.Stderr, "Complete them first, or pass --cascade to complete them too.")
	}
	if code == ExitUsage {
		fmt.Fprintln(c.env.Stderr, "Run `todotil help` for usage.")
	}
	return code
}
