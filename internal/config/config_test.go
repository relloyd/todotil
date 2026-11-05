package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsRoundTrip(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	s, err := LoadSettings(p)
	require.NoError(t, err)
	assert.Equal(t, DefaultSettings(), s)
	assert.FileExists(t, p.Settings())

	s.ShortenLinks = false
	s.UndoDepth = 25
	s.History = History{Sort: "completed", Descending: false}
	require.NoError(t, SaveSettings(p, s))

	got, err := LoadSettings(p)
	require.NoError(t, err)
	assert.Equal(t, s, got)
}

func TestSettingsNormalise(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want func(*Settings)
	}{
		{"missing keys keep defaults", `undo_depth = 5`, func(s *Settings) { s.UndoDepth = 5 }},
		{"bad undo depth", `undo_depth = 0`, func(*Settings) {}},
		{"unknown theme", `theme = "nope"`, func(*Settings) {}},
		{"bad sort", "[history]\nsort = \"title\"\ndescending = false", func(s *Settings) { s.History.Descending = false }},
		{"bad child state", `journal_child_state = "journal"`, func(*Settings) {}},
		{"bad hour", `backup_hour = 24`, func(*Settings) {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Paths{Home: t.TempDir()}
			require.NoError(t, os.WriteFile(p.Settings(), []byte(tt.toml), 0o600))
			got, err := LoadSettings(p)
			require.NoError(t, err)
			want := DefaultSettings()
			tt.want(&want)
			assert.Equal(t, want, got)
		})
	}
}

func TestSettingsParseError(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	require.NoError(t, os.WriteFile(p.Settings(), []byte("undo_depth = ="), 0o600))
	s, err := LoadSettings(p)
	assert.Error(t, err)
	assert.Equal(t, DefaultSettings(), s)
}

func TestKeysRoundTrip(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	km, err := LoadKeys(p)
	require.NoError(t, err)
	assert.Equal(t, DefaultKeyMap(), km)

	km[Undo] = []string{"U"}
	require.NoError(t, SaveKeys(p, km))
	got, err := LoadKeys(p)
	require.NoError(t, err)
	assert.Equal(t, km, got)
}

func TestKeysMergeOverDefaults(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	require.NoError(t, os.WriteFile(p.Keys(), []byte("[keys]\nquit = [\"Q\"]\nbogus = [\"z\"]\n"), 0o600))
	km, err := LoadKeys(p)
	require.NoError(t, err)
	tests := []struct {
		action Action
		key    string
		want   bool
	}{
		{Quit, "Q", true},
		{Quit, "q", false},
		{Down, "j", true},
		{ItemUp, "K", true},
		{ItemDown, "J", true},
		{ItemTop, "ctrl+k", true},
		{ItemBottom, "ctrl+j", true},
		{MoveNext, "x", true},
		{EntryJrnl, "alt+j", true},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, km.Matches(tt.action, tt.key), "%s %s", tt.action, tt.key)
	}
	_, ok := km["bogus"]
	assert.False(t, ok)
}

func TestOldDefaultReorderKeysUpgradeWithoutChangingCustomBindings(t *testing.T) {
	tests := []struct {
		name string
		keys string
		want KeyMap
	}{
		{
			name: "old defaults",
			keys: "item_up = [\"shift+up\", \"alt+k\"]\nitem_down = [\"shift+down\", \"alt+j\"]\n" +
				"item_top = [\"K\"]\nitem_bottom = [\"J\"]\n",
			want: DefaultKeyMap(),
		},
		{
			name: "custom top key",
			keys: "item_up = [\"shift+up\", \"alt+k\"]\nitem_down = [\"shift+down\", \"alt+j\"]\n" +
				"item_top = [\"ctrl+t\"]\nitem_bottom = [\"J\"]\n",
			want: KeyMap{
				ItemUp:     {"shift+up", "alt+k"},
				ItemDown:   {"shift+down", "alt+j"},
				ItemTop:    {"ctrl+t"},
				ItemBottom: {"J"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Paths{Home: t.TempDir()}
			require.NoError(t, os.WriteFile(p.Keys(), []byte("[keys]\n"+tt.keys), 0o600))
			got, err := LoadKeys(p)
			require.NoError(t, err)
			for _, action := range []Action{ItemUp, ItemDown, ItemTop, ItemBottom} {
				assert.Equal(t, tt.want[action], got[action], action)
			}
		})
	}
}

func TestOldDefaultEditorKeysUpgradeWithoutChangingCustomBindings(t *testing.T) {
	tests := []struct {
		name    string
		keys    string
		submit  []string
		newline []string
	}{
		{
			name:    "old defaults",
			keys:    "submit = [\"enter\", \"ctrl+s\"]\nnewline = [\"shift+enter\", \"alt+enter\", \"ctrl+j\"]\n",
			submit:  []string{"ctrl+s"},
			newline: []string{"enter", "shift+enter", "alt+enter", "ctrl+j"},
		},
		{
			name:    "custom save key",
			keys:    "submit = [\"alt+s\"]\nnewline = [\"shift+enter\", \"alt+enter\", \"ctrl+j\"]\n",
			submit:  []string{"alt+s"},
			newline: []string{"shift+enter", "alt+enter", "ctrl+j"},
		},
		{
			name:    "custom newline key",
			keys:    "submit = [\"enter\", \"ctrl+s\"]\nnewline = [\"ctrl+enter\"]\n",
			submit:  []string{"enter", "ctrl+s"},
			newline: []string{"ctrl+enter"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Paths{Home: t.TempDir()}
			require.NoError(t, os.WriteFile(p.Keys(), []byte("[keys]\n"+tt.keys), 0o600))
			got, err := LoadKeys(p)
			require.NoError(t, err)
			assert.Equal(t, tt.submit, got[Submit])
			assert.Equal(t, tt.newline, got[Newline])
			assert.Equal(t, []string{"ctrl+k"}, got[ClearDraft])
		})
	}
}

func TestTheme(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	pal, err := LoadTheme(p, "dawn")
	require.NoError(t, err)
	assert.Equal(t, Themes["dawn"], pal)
	assert.FileExists(t, p.Theme())

	// The template is all comments, so it changes nothing.
	pal, err = LoadTheme(p, "midnight")
	require.NoError(t, err)
	assert.Equal(t, Themes["midnight"], pal)

	require.NoError(t, os.WriteFile(p.Theme(), []byte("[colors]\naccent = \"#123456\"\n"), 0o600))
	pal, err = LoadTheme(p, "midnight")
	require.NoError(t, err)
	assert.Equal(t, "#123456", pal.Accent)
	assert.Equal(t, Themes["midnight"].Text, pal.Text)
}

func TestActionsHaveUniqueNamesAndDefaults(t *testing.T) {
	seen := map[Action]bool{}
	for _, a := range Actions {
		assert.False(t, seen[a.Action], a.Action)
		seen[a.Action] = true
		assert.NotEmpty(t, a.Default, a.Action)
		assert.NotEmpty(t, a.Desc, a.Action)
	}
}
