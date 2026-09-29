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

	"github.com/relloyd/todotil/internal/config"
	"github.com/relloyd/todotil/internal/links"
	"github.com/relloyd/todotil/internal/store"
	"github.com/relloyd/todotil/internal/todo"
	"github.com/relloyd/todotil/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "todotil:", err)
		os.Exit(1)
	}
}

func run() error {
	defHome, err := config.DefaultHome()
	if err != nil {
		return err
	}
	home := flag.String("home", defHome, "app home directory (config, data and backups)")
	flag.Parse()

	paths := config.Paths{Home: *home}
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		return err
	}
	lock, err := store.AcquireLock(paths.Lock())
	if errors.Is(err, store.ErrLocked) {
		return fmt.Errorf("%w (lock: %s)", err, paths.Lock())
	}
	if err != nil {
		return err
	}
	defer lock.Release()

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
