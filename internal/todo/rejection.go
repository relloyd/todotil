package todo

// Reject marks an item rejected. Open descendants require confirmation unless
// force is true; with force they are rejected in the same transaction.
func (s *Service) Reject(id string, force bool) (Result, error) {
	var n int
	err := s.run("reject", true, func(t *tx) error {
		var err error
		n, err = t.reject(id, force)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Item: s.board.Get(id), Followers: n}, nil
}

func (t *tx) reject(id string, force bool) (int, error) {
	it := t.get(id)
	if it == nil {
		return 0, ErrNotFound
	}
	if it.State == Journal {
		return 0, ErrJournalReject
	}
	if it.Done() {
		return 0, ErrCannotRejectDone
	}
	if it.Rejected() {
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

	t.setRejected(it)
	for _, d := range open {
		t.setRejected(t.get(d.ID))
	}
	return len(open), nil
}

// setRejected rejects it (a mutable copy), stages it and mirrors the
// rejection onto its checkbox line.
func (t *tx) setRejected(it *Item) {
	it.markRejected(t.now, t.s.Actor)
	t.put(it)
	if it.Source != "" {
		t.updateSourceLine(it)
	}
}
