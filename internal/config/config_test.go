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
		{ItemTop, "K", true},
		{MoveNext, "x", true},
		{EntryJrnl, "alt+j", true},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, km.Matches(tt.action, tt.key), "%s %s", tt.action, tt.key)
	}
	_, ok := km["bogus"]
	assert.False(t, ok)
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
