package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/relloyd/todotil/internal/todo"
)

// refJSON identifies an item briefly.
type refJSON struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	State    string `json:"state"`
	Done     bool   `json:"done"`
	Assignee string `json:"assignee,omitempty"`
}

func ref(it *todo.Item) refJSON {
	r := refJSON{ID: it.ID, Title: it.Title, State: string(it.State), Done: it.Done()}
	if c := it.ActiveClaim(); c != nil {
		r.Assignee = c.Assignee
	}
	return r
}

// itemJSON is the full representation of an item.
type itemJSON struct {
	Position     int          `json:"position,omitempty"`
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Body         string       `json:"body,omitempty"`
	State        string       `json:"state"`
	Done         bool         `json:"done"`
	Created      time.Time    `json:"created"`
	CreatedBy    string       `json:"created_by,omitempty"`
	Completed    *time.Time   `json:"completed,omitempty"`
	CompletedBy  string       `json:"completed_by,omitempty"`
	Parent       *refJSON     `json:"parent,omitempty"`
	Claim        *todo.Claim  `json:"claim,omitempty"`
	OpenChildren int          `json:"open_children"`
	Children     []itemJSON   `json:"children,omitempty"`
	Claims       []todo.Claim `json:"claims,omitempty"`
	Notes        []todo.Note  `json:"notes,omitempty"`
}

// item builds the JSON for it. full adds body, claim history and notes.
func item(b *todo.Board, it *todo.Item, full bool) itemJSON {
	j := itemJSON{
		ID: it.ID, Title: it.Title, State: string(it.State), Done: it.Done(),
		Created: it.Created, CreatedBy: it.CreatedBy, Completed: it.Completed, CompletedBy: it.CompletedBy,
		Claim: it.ActiveClaim(),
	}
	if p := b.Get(it.Parent); p != nil {
		r := ref(p)
		j.Parent = &r
	}
	for _, c := range b.Descendants(it.ID) {
		if c.Open() {
			j.OpenChildren++
		}
	}
	if full {
		j.Body, j.Claims, j.Notes = it.Body, it.Claims, it.Notes
		for _, c := range b.Children(it.ID) {
			j.Children = append(j.Children, item(b, c, false))
		}
	}
	return j
}

// shortID abbreviates an item ID to its shortest unique prefix. Output is
// produced after any change, so the abbreviations are computed once.
func (c *ctx) shortID(id string) string {
	if c.shorts == nil && c.svc != nil {
		c.shorts = c.svc.Board().ShortIDs()
	}
	if s, ok := c.shorts[id]; ok {
		return s
	}
	return id
}

func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func (c *ctx) claimText(cl *todo.Claim) string {
	if cl == nil {
		return ""
	}
	return fmt.Sprintf("@%s %s", cl.Assignee, age(c.now().Sub(cl.At)))
}

func runList(c *ctx, args []string) error {
	f := newFlags("list", false)
	list := f.String("list", "now", "list to show: now, next or later")
	pos, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("list: unexpected argument %q", pos[0])
	}
	st, err := parseList(*list, false)
	if err != nil {
		return err
	}
	if err := c.open(""); err != nil {
		return err
	}
	b := c.svc.Board()
	rows := b.ViewRows(st)
	var out struct {
		List  string     `json:"list"`
		Items []itemJSON `json:"items"`
	}
	out.List, out.Items = string(st), []itemJSON{}
	// Nest each row under the nearest shallower row.
	var stack []*itemJSON
	for _, r := range rows {
		j := item(b, r.Item, false)
		if r.Depth == 0 {
			j.Position = len(out.Items) + 1
			out.Items = append(out.Items, j)
			stack = []*itemJSON{&out.Items[len(out.Items)-1]}
			continue
		}
		stack = stack[:min(len(stack), r.Depth)]
		p := stack[len(stack)-1]
		p.Children = append(p.Children, j)
		stack = append(stack, &p.Children[len(p.Children)-1])
	}
	return c.print(out, func(w io.Writer) {
		noun := "items"
		if len(out.Items) == 1 {
			noun = "item"
		}
		fmt.Fprintf(w, "%s · %d %s\n", st.Label(), len(out.Items), noun)
		pos := 0
		for _, r := range rows {
			num := "   "
			if r.Depth == 0 {
				pos++
				num = fmt.Sprintf("%3d", pos)
			}
			title := strings.Repeat("  ", r.Depth) + r.Item.Title
			if r.Depth > 0 {
				title = strings.Repeat("  ", r.Depth-1) + "└ " + r.Item.Title
			}
			if r.Context != "" {
				title = r.Context + " › " + title
			}
			line := fmt.Sprintf("%s  %s  %s", num, c.shortID(r.Item.ID), title)
			if cl := c.claimText(r.Item.ActiveClaim()); cl != "" {
				line += "  " + cl
			}
			fmt.Fprintln(w, line)
		}
	})
}

// resolveAny finds an item by item ID or claim ID.
func (c *ctx) resolveAny(refArg string) (*todo.Item, error) {
	b := c.svc.Board()
	if strings.HasPrefix(refArg, "c-") {
		it, _, err := b.FindClaim(refArg)
		return it, err
	}
	return b.Resolve(refArg)
}

func runShow(c *ctx, args []string) error {
	f := newFlags("show", false)
	pos, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("show: expected one item or claim ID")
	}
	if err := c.open(""); err != nil {
		return err
	}
	it, err := c.resolveAny(pos[0])
	if err != nil {
		return err
	}
	b := c.svc.Board()
	j := item(b, it, true)
	return c.print(j, func(w io.Writer) { c.showText(w, j) })
}

func (c *ctx) showText(w io.Writer, j itemJSON) {
	fmt.Fprintf(w, "%s\n", j.Title)
	state := j.State
	if j.Done {
		state = "done"
	}
	fmt.Fprintf(w, "id: %s · %s · created %s", j.ID, state, j.Created.Local().Format("2 Jan 2006 15:04"))
	if j.CreatedBy != "" {
		fmt.Fprintf(w, " by %s", j.CreatedBy)
	}
	if j.Completed != nil {
		fmt.Fprintf(w, " · completed %s", j.Completed.Local().Format("2 Jan 2006 15:04"))
		if j.CompletedBy != "" {
			fmt.Fprintf(w, " by %s", j.CompletedBy)
		}
	}
	fmt.Fprintln(w)
	if j.Claim != nil {
		fmt.Fprintf(w, "claimed by %s (%s) · claim %s\n", j.Claim.Assignee, age(c.now().Sub(j.Claim.At)), j.Claim.ID)
	}
	if j.Parent != nil {
		fmt.Fprintf(w, "parent: %s  %s\n", c.shortID(j.Parent.ID), j.Parent.Title)
	}
	if j.Body != "" {
		fmt.Fprintf(w, "\n%s\n", j.Body)
	}
	if len(j.Children) > 0 {
		fmt.Fprintln(w, "\nchildren:")
		for _, k := range j.Children {
			box := "[ ]"
			if k.Done {
				box = "[x]"
			}
			line := fmt.Sprintf("  %s %s  %s (%s)", box, c.shortID(k.ID), k.Title, k.State)
			if k.Claim != nil {
				line += "  @" + k.Claim.Assignee
			}
			fmt.Fprintln(w, line)
		}
	}
	if len(j.Notes) > 0 {
		fmt.Fprintln(w, "\nnotes:")
		for _, n := range j.Notes {
			fmt.Fprintf(w, "  %s %s: %s\n", n.At.Local().Format("2 Jan 15:04"), n.By, n.Text)
		}
	}
}

func runClaim(c *ctx, args []string) error {
	f := newFlags("claim", true)
	list := f.String("list", "now", "list that positions and next refer to")
	steal := f.Bool("steal", false, "take over a claim held by someone else")
	pos, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("claim: expected one position, item ID or \"next\"")
	}
	target, err := todo.ParseTarget(pos[0])
	if err != nil {
		return usagef("claim: %v", err)
	}
	st, err := parseList(*list, false)
	if err != nil {
		return err
	}
	who, err := c.actor(f)
	if err != nil {
		return err
	}
	if err := c.open(who); err != nil {
		return err
	}
	res, err := c.svc.Claim(target, st, *steal)
	if err != nil {
		return err
	}
	out := struct {
		Claim       string   `json:"claim"`
		Assignee    string   `json:"assignee"`
		AlreadyHeld bool     `json:"already_held"`
		StoleFrom   string   `json:"stole_from,omitempty"`
		MovedFrom   string   `json:"moved_from,omitempty"`
		Item        itemJSON `json:"item"`
	}{res.Claim.ID, res.Claim.Assignee, res.AlreadyHeld, res.StoleFrom, string(res.MovedFrom), item(c.svc.Board(), res.Item, true)}
	return c.print(out, func(w io.Writer) {
		verb := "Claimed"
		if res.AlreadyHeld {
			verb = "Already holding"
		}
		fmt.Fprintf(w, "%s %q as %s\nclaim: %s\n", verb, res.Item.Title, who, res.Claim.ID)
		if res.StoleFrom != "" {
			fmt.Fprintf(w, "(taken over from %s)\n", res.StoleFrom)
		}
		if res.MovedFrom != "" {
			fmt.Fprintf(w, "(moved from %s to Now)\n", res.MovedFrom.Label())
		}
		fmt.Fprintln(w)
		c.showText(w, out.Item)
	})
}

func runDone(c *ctx, args []string) error {
	f := newFlags("done", false)
	cascade := f.Bool("cascade", false, "also complete open children")
	note := f.String("note", "", "record a final note")
	pos, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("done: expected one claim ID")
	}
	if err := c.openForClaim(pos[0]); err != nil {
		return err
	}
	res, err := c.svc.Finish(pos[0], *cascade, *note)
	if err != nil {
		return err
	}
	out := struct {
		OK          bool       `json:"ok"`
		Claim       string     `json:"claim"`
		AlreadyDone bool       `json:"already_done"`
		Completed   *time.Time `json:"completed,omitempty"`
		CompletedBy string     `json:"completed_by"`
		Cascaded    int        `json:"cascaded"`
		Item        refJSON    `json:"item"`
	}{true, res.Claim.ID, res.AlreadyDone, res.Item.Completed, completedBy(res.Item), res.Cascaded, ref(res.Item)}
	return c.print(out, func(w io.Writer) {
		switch {
		case res.AlreadyDone:
			fmt.Fprintf(w, "%q was already completed by %s\n", res.Item.Title, out.CompletedBy)
		case res.Cascaded > 0:
			fmt.Fprintf(w, "Completed %q and %d open children\n", res.Item.Title, res.Cascaded)
		default:
			fmt.Fprintf(w, "Completed %q\n", res.Item.Title)
		}
	})
}

// completedBy names who completed an item; the TUI user is "user".
func completedBy(it *todo.Item) string {
	if !it.Done() {
		return ""
	}
	if it.CompletedBy == "" {
		return "user"
	}
	return it.CompletedBy
}

// openForClaim opens the data acting as the claim's assignee, so changes are
// attributed to them even without --as.
func (c *ctx) openForClaim(claimID string) error {
	if err := c.open(""); err != nil {
		return err
	}
	_, cl, err := c.svc.Board().FindClaim(claimID)
	if err != nil {
		return err
	}
	c.svc.Actor = cl.Assignee
	return nil
}

func runRelease(c *ctx, args []string) error {
	f := newFlags("release", false)
	reason := f.String("reason", "", "why the item is being released")
	pos, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("release: expected one claim ID")
	}
	if err := c.openForClaim(pos[0]); err != nil {
		return err
	}
	it, err := c.svc.Release(pos[0], *reason)
	if err != nil {
		return err
	}
	out := struct {
		OK    bool    `json:"ok"`
		Claim string  `json:"claim"`
		Item  refJSON `json:"item"`
	}{true, pos[0], ref(it)}
	return c.print(out, func(w io.Writer) { fmt.Fprintf(w, "Released %q\n", it.Title) })
}

func runNote(c *ctx, args []string) error {
	f := newFlags("note", false)
	pos, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return usagef("note: expected a claim ID and the note text")
	}
	if err := c.openForClaim(pos[0]); err != nil {
		return err
	}
	it, note, err := c.svc.AddNote(pos[0], strings.Join(pos[1:], " "))
	if err != nil {
		return err
	}
	out := struct {
		OK    bool      `json:"ok"`
		Claim string    `json:"claim"`
		Item  refJSON   `json:"item"`
		Note  todo.Note `json:"note"`
	}{true, pos[0], ref(it), note}
	return c.print(out, func(w io.Writer) { fmt.Fprintf(w, "Noted on %q\n", it.Title) })
}

func runAdd(c *ctx, args []string) error {
	f := newFlags("add", true)
	list := f.String("list", "", "now, next, later or journal (default: the parent's list if it's open, else now)")
	parent := f.String("parent", "", "item ID to add under")
	under := f.String("under", "", "claim ID whose item to add under")
	pos, err := f.parse(args)
	if err != nil {
		return err
	}
	text := strings.Join(pos, " ")
	if text == "-" {
		b, err := io.ReadAll(c.env.Stdin)
		if err != nil {
			return err
		}
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		return usagef("add: expected the entry text (or - to read it from stdin)")
	}
	if *parent != "" && *under != "" {
		return usagef("add: use --parent or --under, not both")
	}
	who, err := c.actor(f)
	if err != nil {
		return err
	}
	if err := c.open(who); err != nil {
		return err
	}
	b := c.svc.Board()
	var p *todo.Item
	switch {
	case *parent != "":
		p, err = b.Resolve(*parent)
	case *under != "":
		p, _, err = b.FindClaim(*under)
	}
	if err != nil {
		return err
	}
	st := todo.Now
	if p != nil && p.Open() {
		st = p.State
	}
	if *list != "" {
		if st, err = parseList(*list, true); err != nil {
			return err
		}
	}
	parentID := ""
	if p != nil {
		parentID = p.ID
	}
	childState := st
	if !st.Active() {
		childState = todo.Now
	}
	res, err := c.svc.Add(text, st, parentID, childState)
	if err != nil {
		return err
	}
	j := item(c.svc.Board(), res.Item, true)
	return c.print(struct {
		Item itemJSON `json:"item"`
	}{j}, func(w io.Writer) {
		fmt.Fprintf(w, "Added %q to %s\nid: %s\n", res.Item.Title, res.Item.State.Label(), res.Item.ID)
		if n := len(j.Children); n > 0 {
			fmt.Fprintf(w, "with %d checkbox children\n", n)
		}
	})
}

func runHelp(c *ctx, args []string) error {
	if len(args) > 0 && args[0] == "agents" {
		fmt.Fprint(c.env.Stdout, AgentGuide)
		return nil
	}
	w := c.env.Stdout
	fmt.Fprintln(w, "todotil: terminal todo and meeting-notes tracker")
	fmt.Fprintln(w, "\nRun with no command to open the TUI. Commands for agents and scripts:")
	for _, cmd := range commands {
		fmt.Fprintf(w, "  todotil %s\n", cmd.summary)
	}
	fmt.Fprintln(w, "\nGlobal flags go before the command: --home DIR (default ~/.config/todotil).")
	fmt.Fprintln(w, "Run `todotil help agents` for the agent workflow and exit codes.")
	return nil
}
