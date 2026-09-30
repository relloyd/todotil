// Command todotil is a terminal todo and meeting-notes tracker built around a
// Now / Next / Later workflow.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"

	"github.com/relloyd/todotil/internal/cli"
	"github.com/relloyd/todotil/internal/config"
	"github.com/relloyd/todotil/internal/links"
	"github.com/relloyd/todotil/internal/store"
	"github.com/relloyd/todotil/internal/todo"
	"github.com/relloyd/todotil/internal/ui"
)

func main() {
	os.Exit(run())
}

func run() int {
	defHome, err := config.DefaultHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, "todotil:", err)
		return 1
	}
	home := flag.String("home", defHome, "app home directory (config, data and backups)")
	flag.Usage = func() {
		cli.Run(cli.Env{Stdout: os.Stderr}, []string{"help"})
	}
	flag.Parse()
	paths := config.Paths{Home: *home}

	if err := prepare(paths); err != nil {
		fmt.Fprintln(os.Stderr, "todotil:", err)
		return 1
	}
	if args := flag.Args(); len(args) > 0 {
		if !cli.IsCommand(args[0]) && args[0] != "-h" && args[0] != "--help" {
			fmt.Fprintf(os.Stderr, "todotil: unknown command %q\n", args[0])
			flag.Usage()
			return cli.ExitUsage
		}
		return cli.Run(cli.Env{
			Paths:  paths,
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
			Getenv: os.Getenv,
		}, args)
	}
	if err := runTUI(paths); err != nil {
		fmt.Fprintln(os.Stderr, "todotil:", err)
		return 1
	}
	return 0
}

// prepare creates the home directory and refuses to run next to an older
// binary that still holds a session-long lock.
func prepare(paths config.Paths) error {
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		return err
	}
	if err := store.CheckLegacyLock(paths.Lock()); err != nil {
		if errors.Is(err, store.ErrLocked) {
			return fmt.Errorf("%w (lock: %s)", err, paths.Lock())
		}
		return err
	}
	return nil
}

func runTUI(paths config.Paths) error {
	// Config problems are reported in the status bar rather than stopping
	// the app; defaults are used instead.
	var warnings []string
	settings, settingsErr := config.LoadSettings(paths)
	if settingsErr != nil {
		warnings = append(warnings, settingsErr.Error())
	}
	keys, keysErr := config.LoadKeys(paths)
	if keysErr != nil {
		warnings = append(warnings, keysErr.Error())
	}
	palette, err := config.LoadTheme(paths, settings.Theme)
	if err != nil {
		warnings = append(warnings, err.Error())
	}

	log, loaded, err := store.Open(paths.Data())
	if err != nil {
		return err
	}
	defer log.Close()
	if loaded.Skipped > 0 {
		warnings = append(warnings, fmt.Sprintf("skipped %d unreadable line(s) in %s", loaded.Skipped, paths.Data()))
	}
	board := todo.NewBoard()
	for _, e := range loaded.Events {
		board.Replay(e)
	}

	model := ui.New(ui.Deps{
		Service:   todo.NewService(board, log, settings.UndoDepth),
		Log:       log,
		Paths:     paths,
		Settings:  settings,
		Keys:      keys,
		Palette:   palette,
		Fetcher:   links.NewFetcher(),
		Clipboard: clipboard.WriteAll,
		Warnings:  warnings,

		SettingsInvalid: settingsErr != nil,
		KeysInvalid:     keysErr != nil,
	})
	_, err = tea.NewProgram(model).Run()
	return err
}
