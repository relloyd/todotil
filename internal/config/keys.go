package config

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Action is something a key can be bound to.
type Action string

// Actions. The move and entry groups are read after "m" and inside the add
// dialog respectively, so their keys may overlap with list keys.
const (
	Quit          Action = "quit"
	NextView      Action = "next_view"
	PrevView      Action = "prev_view"
	ViewNow       Action = "view_now"
	ViewNext      Action = "view_next"
	ViewLater     Action = "view_later"
	ViewHistory   Action = "view_history"
	Down          Action = "down"
	Up            Action = "up"
	Top           Action = "top"
	Bottom        Action = "bottom"
	HalfDown      Action = "half_page_down"
	HalfUp        Action = "half_page_up"
	PageDown      Action = "page_down"
	PageUp        Action = "page_up"
	JumpParent    Action = "jump_parent"
	JumpChild     Action = "jump_child"
	JumpBack      Action = "jump_back"
	OpenSettings  Action = "settings"
	Help          Action = "help"
	Add           Action = "add"
	AddChild      Action = "add_child"
	Edit          Action = "edit"
	Open          Action = "open"
	Done          Action = "toggle_done"
	Reject        Action = "reject"
	Delete        Action = "delete"
	Undo          Action = "undo"
	Copy          Action = "copy"
	CopyID        Action = "copy_id"
	Unassign      Action = "unassign"
	SelectMode    Action = "select_mode"
	ItemUp        Action = "item_up"
	ItemDown      Action = "item_down"
	ItemTop       Action = "item_top"
	ItemBottom    Action = "item_bottom"
	Indent        Action = "indent"
	Outdent       Action = "outdent"
	SortKey       Action = "history_sort_key"
	SortDir       Action = "history_sort_direction"
	HistoryFilter Action = "history_filter"
	Move          Action = "move"
	MoveNow       Action = "move_now"
	MoveNext      Action = "move_next"
	MoveLater     Action = "move_later"
	MoveJournal   Action = "move_journal"
	EntryNow      Action = "entry_now"
	EntryNext     Action = "entry_next"
	EntryLater    Action = "entry_later"
	EntryJrnl     Action = "entry_journal"
	Submit        Action = "submit"
	Newline       Action = "newline"
	Cancel        Action = "cancel"
)

// ActionInfo describes an action for help and the settings pane.
type ActionInfo struct {
	Action  Action
	Group   string
	Desc    string
	Default []string
}

// Action groups.
const (
	GroupGlobal = "Global"
	GroupList   = "Lists"
	GroupMove   = "After m (move)"
	GroupEntry  = "Add/edit dialog"
)

// Actions lists every bindable action with its defaults.
var Actions = []ActionInfo{
	{Quit, GroupGlobal, "Quit", []string{"q", "ctrl+c"}},
	{NextView, GroupGlobal, "Next view", []string{"tab"}},
	{PrevView, GroupGlobal, "Previous view", []string{"shift+tab"}},
	{ViewNow, GroupGlobal, "Go to Now", []string{"1"}},
	{ViewNext, GroupGlobal, "Go to Next", []string{"2"}},
	{ViewLater, GroupGlobal, "Go to Later", []string{"3"}},
	{ViewHistory, GroupGlobal, "Go to History", []string{"4"}},
	{OpenSettings, GroupGlobal, "Open settings", []string{","}},
	{Help, GroupGlobal, "Show all key bindings", []string{"?"}},
	{Undo, GroupGlobal, "Undo last change", []string{"u", "ctrl+z"}},
	{SelectMode, GroupGlobal, "Release the mouse for text selection", []string{"v"}},

	{Down, GroupList, "Cursor down", []string{"j", "down"}},
	{Up, GroupList, "Cursor up", []string{"k", "up"}},
	{Top, GroupList, "Cursor to top", []string{"g", "home"}},
	{Bottom, GroupList, "Cursor to bottom", []string{"G", "end"}},
	{HalfDown, GroupList, "Half page down", []string{"ctrl+d"}},
	{HalfUp, GroupList, "Half page up", []string{"ctrl+u"}},
	{PageDown, GroupList, "Page down", []string{"pgdown"}},
	{PageUp, GroupList, "Page up", []string{"pgup"}},
	{JumpParent, GroupList, "Jump to parent", []string{"p"}},
	{JumpChild, GroupList, "Jump to first child", []string{"c"}},
	{JumpBack, GroupList, "Jump back", []string{"ctrl+o"}},
	{Add, GroupList, "Add an entry", []string{"a"}},
	{AddChild, GroupList, "Add a child of the selected item", []string{"A"}},
	{Edit, GroupList, "Edit the selected item", []string{"e"}},
	{Open, GroupList, "Open the detail view", []string{"enter"}},
	{Done, GroupList, "Toggle done", []string{"x", "space"}},
	{Reject, GroupList, "Reject item and open children", []string{"alt+x"}},
	{Delete, GroupList, "Delete the selected item", []string{"D"}},
	{Copy, GroupList, "Copy item (and children) to the clipboard", []string{"y"}},
	{CopyID, GroupList, "Copy the selected item's ID to the clipboard", []string{"ctrl+y"}},
	{Unassign, GroupList, "Unassign: end an agent's claim", []string{"U"}},
	{ItemUp, GroupList, "Move item up", []string{"shift+up", "alt+k"}},
	{ItemDown, GroupList, "Move item down", []string{"shift+down", "alt+j"}},
	{ItemTop, GroupList, "Send item to top", []string{"K"}},
	{ItemBottom, GroupList, "Send item to bottom", []string{"J"}},
	{Indent, GroupList, "Indent under the item above", []string{">"}},
	{Outdent, GroupList, "Outdent one level", []string{"<"}},
	{SortKey, GroupList, "History: toggle created/completed/rejected sort", []string{"s"}},
	{SortDir, GroupList, "History: reverse sort direction", []string{"r"}},
	{HistoryFilter, GroupList, "History: cycle outcome filter", []string{"f"}},
	{Move, GroupList, "Start a move", []string{"m"}},

	{MoveNow, GroupMove, "Move to Now", []string{"n"}},
	{MoveNext, GroupMove, "Move to Next", []string{"x"}},
	{MoveLater, GroupMove, "Move to Later", []string{"l"}},
	{MoveJournal, GroupMove, "Move to Journal", []string{"j"}},

	{EntryNow, GroupEntry, "Classify as Now", []string{"alt+n"}},
	{EntryNext, GroupEntry, "Classify as Next", []string{"alt+x"}},
	{EntryLater, GroupEntry, "Classify as Later", []string{"alt+l"}},
	{EntryJrnl, GroupEntry, "Classify as Journal", []string{"alt+j"}},
	{Submit, GroupEntry, "Save the entry", []string{"enter", "ctrl+s"}},
	{Newline, GroupEntry, "Insert a new line", []string{"shift+enter", "alt+enter", "ctrl+j"}},
	{Cancel, GroupEntry, "Cancel", []string{"esc"}},
}

// Info returns the description of a.
func Info(a Action) ActionInfo {
	for _, ai := range Actions {
		if ai.Action == a {
			return ai
		}
	}
	return ActionInfo{Action: a}
}

// KeyMap maps actions to key strings as reported by Bubble Tea.
type KeyMap map[Action][]string

// DefaultKeyMap returns the default bindings.
func DefaultKeyMap() KeyMap {
	km := KeyMap{}
	for _, a := range Actions {
		km[a.Action] = slices.Clone(a.Default)
	}
	return km
}

// Matches reports whether key is bound to a.
func (km KeyMap) Matches(a Action, keys ...string) bool {
	for _, k := range keys {
		if k != "" && slices.Contains(km[a], k) {
			return true
		}
	}
	return false
}

// First returns the first key bound to a, for display.
func (km KeyMap) First(a Action) string {
	if ks := km[a]; len(ks) > 0 {
		return ks[0]
	}
	return ""
}

// Help returns the bound keys joined for display.
func (km KeyMap) Help(a Action) string { return strings.Join(km[a], "/") }

// LoadKeys reads keys.toml, merging it over the defaults. The file is
// created with every default binding if missing.
func LoadKeys(p Paths) (KeyMap, error) {
	km := DefaultKeyMap()
	b, err := os.ReadFile(p.Keys())
	if os.IsNotExist(err) {
		return km, SaveKeys(p, km)
	}
	if err != nil {
		return km, err
	}
	var file struct {
		Keys map[string][]string `toml:"keys"`
	}
	if _, err := toml.Decode(string(b), &file); err != nil {
		return km, fmt.Errorf("%s: %w", p.Keys(), err)
	}
	for name, keys := range file.Keys {
		if _, ok := km[Action(name)]; ok {
			km[Action(name)] = keys
		}
	}
	return km, nil
}

// SaveKeys writes keys.toml atomically, grouped and commented.
func SaveKeys(p Paths, km KeyMap) error {
	var buf bytes.Buffer
	buf.WriteString("# todotil key bindings. Each action maps to a list of keys, written as\n")
	buf.WriteString("# Bubble Tea reports them: \"a\", \"K\", \"ctrl+o\", \"alt+j\", \"shift+up\", \"space\", \"enter\".\n")
	buf.WriteString("# Rebind in the app from settings (\",\") or edit here while todotil is closed.\n\n[keys]\n")
	group := ""
	for _, a := range Actions {
		if a.Group != group {
			group = a.Group
			fmt.Fprintf(&buf, "\n# %s\n", group)
		}
		quoted := make([]string, len(km[a.Action]))
		for i, k := range km[a.Action] {
			quoted[i] = strconv.Quote(k)
		}
		fmt.Fprintf(&buf, "%-24s = [%s] # %s\n", a.Action, strings.Join(quoted, ", "), a.Desc)
	}
	return WriteFileAtomic(p.Keys(), buf.Bytes())
}
