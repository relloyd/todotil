package todo

// Complete marks an item done. If it has open descendants and force is
// false, a *NeedsConfirmError is returned and nothing changes; with force
// they are completed too.
func (s *Service) Complete(id string, force bool) (Result, error) {
	var n int
	err := s.run("complete", true, func(t *tx) error {
		var err error
		n, err = t.complete(id, force)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Item: s.board.Get(id), Followers: n}, nil
}

func (t *tx) complete(id string, force bool) (int, error) {
	it := t.get(id)
	if it == nil {
		return 0, ErrNotFound
	}
	if it.State == Journal {
		return 0, ErrJournalDone
	}
	if it.Rejected() {
		return 0, ErrRejected
	}
	if it.Done() {
		return 0, nil
	}
	var open []*Item
	for _, d := range t.w.Descendants(id) {
		if d.Open() {
			open = append(open, d)
		}
	}
	if len(open) > 0 && !force {
		return 0, &NeedsConfirmError{Open: len(open), Children: open}
	}
	t.setDone(it, true)
	for _, d := range open {
		t.setDone(t.get(d.ID), true)
	}
	return len(open), nil
}

// Reopen clears an item's completed or rejected outcome.
func (s *Service) Reopen(id string) (Result, error) {
	err := s.run("reopen", true, func(t *tx) error {
		it := t.get(id)
		if it == nil {
			return ErrNotFound
		}
		t.setDone(it, false)
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Item: s.board.Get(id)}, nil
}

// setDone sets or clears the outcome on it (a mutable copy), stages it and
// mirrors the outcome onto its checkbox line. Clearing also reopens a
// rejected item.
func (t *tx) setDone(it *Item, done bool) {
	switch {
	case done && !it.Done():
		it.markDone(t.now, t.s.Actor)
	case !done && (it.Done() || it.Rejected()):
		it.clearOutcome()
	}
	t.put(it)
	if it.Source != "" {
		t.updateSourceLine(it)
	}
}
