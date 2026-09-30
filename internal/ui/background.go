package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/todotil/internal/links"
	"github.com/relloyd/todotil/internal/store"
	"github.com/relloyd/todotil/internal/todo"
)

type linkTitleMsg struct {
	url, title string
	err        error
}

// fetchVisibleLinks starts title fetches for links on screen that have no
// cached title yet.
func (m *Model) fetchVisibleLinks() tea.Cmd {
	if !m.Settings.ShortenLinks || m.Fetcher == nil {
		return nil
	}
	var texts []string
	switch m.mode {
	case modeDetail:
		if it := m.board().Get(m.detail.id); it != nil {
			texts = append(texts, it.Title, it.Body)
			for _, k := range m.board().Descendants(it.ID) {
				texts = append(texts, k.Title)
			}
		}
	case modeList:
		lines := m.lines[m.tab]
		for i := m.offset[m.tab]; i < len(lines) && i < m.offset[m.tab]+m.contentHeight(); i++ {
			if r := lines[i].row; r >= 0 && m.rows[m.tab][r].Kind == todo.RowItem {
				texts = append(texts, m.rows[m.tab][r].Item.Title)
			}
		}
	}
	var cmds []tea.Cmd
	for _, t := range texts {
		for _, sp := range links.Find(t) {
			u := sp.URL
			if sp.Label != "" || m.linkInflight[u] || m.linkFailed[u] {
				continue
			}
			if _, ok := m.board().LinkTitle(u); ok {
				continue
			}
			m.linkInflight[u] = true
			cmds = append(cmds, m.fetchTitle(u))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) fetchTitle(url string) tea.Cmd {
	f, sem := m.Fetcher, m.linkSem
	return func() tea.Msg {
		sem <- struct{}{}
		defer func() { <-sem }()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		title, err := f.Title(ctx, url)
		return linkTitleMsg{url: url, title: title, err: err}
	}
}

func (m *Model) handleLinkTitle(msg linkTitleMsg) tea.Cmd {
	delete(m.linkInflight, msg.url)
	if msg.err != nil {
		// Keep showing the raw URL; don't retry this session.
		m.linkFailed[msg.url] = true
		return nil
	}
	if err := m.Service.SetLinkTitle(msg.url, msg.title); err != nil {
		return m.fail(err)
	}
	if m.mode == modeDetail {
		m.buildDetail()
	}
	return nil
}

type (
	backupTickMsg time.Time
	backupDoneMsg struct {
		path string
		err  error
	}
)

// backupCheckInterval is how often the scheduler checks whether the daily
// backup is due. Polling survives sleep and clock changes better than one
// long timer.
const backupCheckInterval = time.Minute

func (m *Model) nextBackupAfter(now time.Time) time.Time {
	return store.NextBackupTime(now, m.Settings.BackupHour)
}

// startupBackupCmd backs up immediately if there is no backup from the last
// 24 hours.
func (m *Model) startupBackupCmd() tea.Cmd {
	if m.Log == nil {
		return nil
	}
	m.nextBackup = m.nextBackupAfter(m.Now())
	log, dir, keep, now := m.Log, m.Paths.Backups(), m.Settings.BackupKeepDays, m.Now()
	return func() tea.Msg {
		need, err := store.NeedsBackup(dir, now, 24*time.Hour)
		if err != nil || !need {
			return backupDoneMsg{err: err}
		}
		return runBackup(log, dir, keep, now)
	}
}

func runBackup(log *store.Log, dir string, keep int, now time.Time) backupDoneMsg {
	path, err := log.Backup(dir, now)
	if err != nil {
		return backupDoneMsg{err: err}
	}
	_, err = store.Prune(dir, now, keep)
	return backupDoneMsg{path: path, err: err}
}

func (m *Model) backupTick() tea.Cmd {
	if m.Log == nil {
		return nil
	}
	return tea.Tick(backupCheckInterval, func(t time.Time) tea.Msg { return backupTickMsg(t) })
}

func (m *Model) handleBackupTick(now time.Time) tea.Cmd {
	next := m.backupTick()
	if m.nextBackup.IsZero() || now.Before(m.nextBackup) {
		return next
	}
	m.nextBackup = m.nextBackupAfter(now)
	log, dir, keep := m.Log, m.Paths.Backups(), m.Settings.BackupKeepDays
	return tea.Batch(next, func() tea.Msg { return runBackup(log, dir, keep, now) })
}

type syncTickMsg struct{}

// syncInterval is how often the TUI looks for changes made by agents.
const syncInterval = 500 * time.Millisecond

func (m *Model) syncTick() tea.Cmd {
	return tea.Tick(syncInterval, func(time.Time) tea.Msg { return syncTickMsg{} })
}

// handleSyncTick reloads changes other processes wrote to the log.
func (m *Model) handleSyncTick() tea.Cmd {
	next := m.syncTick()
	changed, err := m.Service.Sync()
	if err != nil {
		if err.Error() == m.lastSyncErr {
			return next
		}
		m.lastSyncErr = err.Error()
		return tea.Batch(next, m.fail(err))
	}
	m.lastSyncErr = ""
	if changed {
		m.refresh()
	}
	return next
}
