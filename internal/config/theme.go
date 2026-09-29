package config

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"slices"

	"github.com/BurntSushi/toml"
)

// Palette holds a theme's colours as hex strings.
type Palette struct {
	Text      string `toml:"text"`
	Muted     string `toml:"muted"`
	Subtle    string `toml:"subtle"`
	Accent    string `toml:"accent"`
	AccentAlt string `toml:"accent_alt"`
	Selection string `toml:"selection"`
	Now       string `toml:"now"`
	Next      string `toml:"next"`
	Later     string `toml:"later"`
	Journal   string `toml:"journal"`
	Done      string `toml:"done"`
	Link      string `toml:"link"`
	Warn      string `toml:"warn"`
	Error     string `toml:"error"`
	Dialog    string `toml:"dialog"`
}

// DefaultThemeName is the theme used unless configured otherwise.
const DefaultThemeName = "midnight"

// Themes are the built-in palettes.
var Themes = map[string]Palette{
	"midnight": {
		Text: "#c8d3f5", Muted: "#828bb8", Subtle: "#3b4261", Accent: "#82aaff",
		AccentAlt: "#c099ff", Selection: "#2d3f76", Now: "#ff966c", Next: "#82aaff",
		Later: "#c099ff", Journal: "#4fd6be", Done: "#c3e88d", Link: "#65bcff",
		Warn: "#ffc777", Error: "#ff757f", Dialog: "#1e2030",
	},
	"dawn": {
		Text: "#3760bf", Muted: "#6172b0", Subtle: "#c4c8da", Accent: "#2e7de9",
		AccentAlt: "#9854f1", Selection: "#b7c1e3", Now: "#b15c00", Next: "#2e7de9",
		Later: "#9854f1", Journal: "#118c74", Done: "#587539", Link: "#007197",
		Warn: "#8c6c3e", Error: "#c64343", Dialog: "#e9e9ed",
	},
	"ember": {
		Text: "#e0def4", Muted: "#908caa", Subtle: "#403d52", Accent: "#ebbcba",
		AccentAlt: "#c4a7e7", Selection: "#393552", Now: "#eb6f92", Next: "#f6c177",
		Later: "#c4a7e7", Journal: "#9ccfd8", Done: "#3e8fb0", Link: "#9ccfd8",
		Warn: "#f6c177", Error: "#eb6f92", Dialog: "#1f1d2e",
	},
}

// ThemeNames returns the built-in theme names in a stable order.
func ThemeNames() []string {
	names := make([]string, 0, len(Themes))
	for n := range Themes {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// LoadTheme returns the named built-in palette with any colours set in
// theme.toml applied on top. A commented template is written if missing.
func LoadTheme(p Paths, name string) (Palette, error) {
	pal, ok := Themes[name]
	if !ok {
		pal = Themes[DefaultThemeName]
	}
	b, err := os.ReadFile(p.Theme())
	if os.IsNotExist(err) {
		return pal, writeThemeTemplate(p)
	}
	if err != nil {
		return pal, err
	}
	var file struct {
		Colors Palette `toml:"colors"`
	}
	if _, err := toml.Decode(string(b), &file); err != nil {
		return pal, fmt.Errorf("%s: %w", p.Theme(), err)
	}
	over := reflect.ValueOf(file.Colors)
	dst := reflect.ValueOf(&pal).Elem()
	for i := range over.NumField() {
		if v := over.Field(i).String(); v != "" {
			dst.Field(i).SetString(v)
		}
	}
	return pal, nil
}

func writeThemeTemplate(p Paths) error {
	var buf bytes.Buffer
	buf.WriteString("# todotil theme overrides. Pick a base theme in settings (\",\"), then\n")
	buf.WriteString("# uncomment any colour below to override it. Values are hex colours.\n\n[colors]\n")
	pal := Themes[DefaultThemeName]
	v, t := reflect.ValueOf(pal), reflect.TypeOf(pal)
	for i := range v.NumField() {
		fmt.Fprintf(&buf, "# %s = %q\n", t.Field(i).Tag.Get("toml"), v.Field(i).String())
	}
	return WriteFileAtomic(p.Theme(), buf.Bytes())
}
