// Package config loads and saves the plain-text settings, key bindings and
// theme files in the app home directory.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Paths locates everything under the app home directory.
type Paths struct {
	Home string
}

// DefaultHome returns $TODOTIL_HOME or ~/.config/todotil.
func DefaultHome() (string, error) {
	if h := os.Getenv("TODOTIL_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "todotil"), nil
}

func (p Paths) Settings() string { return filepath.Join(p.Home, "config.toml") }
func (p Paths) Keys() string     { return filepath.Join(p.Home, "keys.toml") }
func (p Paths) Theme() string    { return filepath.Join(p.Home, "theme.toml") }
func (p Paths) Data() string     { return filepath.Join(p.Home, "data") }
func (p Paths) Backups() string  { return filepath.Join(p.Home, "backups") }

// Lock is the session lock held by versions before the agent CLI. Newer
// versions only check it, to avoid running alongside an old binary.
func (p Paths) Lock() string { return filepath.Join(p.Home, "todotil.lock") }

// History holds the History view's sort preference.
type History struct {
	Sort       string `toml:"sort"` // "created" or "completed"
	Descending bool   `toml:"descending"`
}

// Settings is the content of config.toml.
type Settings struct {
	ShortenLinks      bool    `toml:"shorten_links"`
	UndoDepth         int     `toml:"undo_depth"`
	Mouse             bool    `toml:"mouse"`
	Theme             string  `toml:"theme"`
	JournalChildState string  `toml:"journal_child_state"`
	BackupHour        int     `toml:"backup_hour"`
	BackupKeepDays    int     `toml:"backup_keep_days"`
	History           History `toml:"history"`
}

// DefaultSettings returns the settings used when config.toml is missing.
func DefaultSettings() Settings {
	return Settings{
		ShortenLinks:      true,
		UndoDepth:         10,
		Mouse:             true,
		Theme:             DefaultThemeName,
		JournalChildState: "now",
		BackupHour:        11,
		BackupKeepDays:    10,
		History:           History{Sort: "created", Descending: true},
	}
}

// Normalise clamps out-of-range values back to sensible ones.
func (s *Settings) Normalise() {
	d := DefaultSettings()
	if s.UndoDepth < 1 {
		s.UndoDepth = d.UndoDepth
	}
	s.UndoDepth = min(s.UndoDepth, 1000)
	if _, ok := Themes[s.Theme]; !ok {
		s.Theme = d.Theme
	}
	switch s.JournalChildState {
	case "now", "next", "later":
	default:
		s.JournalChildState = d.JournalChildState
	}
	if s.BackupHour < 0 || s.BackupHour > 23 {
		s.BackupHour = d.BackupHour
	}
	if s.BackupKeepDays < 1 {
		s.BackupKeepDays = d.BackupKeepDays
	}
	if s.History.Sort != "created" && s.History.Sort != "completed" {
		s.History.Sort = d.History.Sort
	}
}

const settingsHeader = `# todotil settings. Edit in the app with "," or by hand while it is closed.
`

// LoadSettings reads config.toml, creating it with defaults if missing.
func LoadSettings(p Paths) (Settings, error) {
	s := DefaultSettings()
	b, err := os.ReadFile(p.Settings())
	if os.IsNotExist(err) {
		return s, SaveSettings(p, s)
	}
	if err != nil {
		return s, err
	}
	if _, err := toml.Decode(string(b), &s); err != nil {
		return DefaultSettings(), fmt.Errorf("%s: %w", p.Settings(), err)
	}
	s.Normalise()
	return s, nil
}

// SaveSettings writes config.toml atomically.
func SaveSettings(p Paths, s Settings) error {
	var buf bytes.Buffer
	buf.WriteString(settingsHeader)
	if err := toml.NewEncoder(&buf).Encode(s); err != nil {
		return err
	}
	return WriteFileAtomic(p.Settings(), buf.Bytes())
}

// WriteFileAtomic writes data to a temp file in the same directory, syncs it
// and renames it over path.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
