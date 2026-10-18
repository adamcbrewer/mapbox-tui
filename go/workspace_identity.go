package main

// Live identity and edit revisions guard commands against replacement and human
// edits. They are independent of the persisted checkpoint revision owned by the
// workspace store. Replacing a Workspace starts a new live identity; clear does
// so explicitly while keeping document IDs monotonic within the running viewer.
func (w *Workspace) liveIdentity() string {
	if w.liveGeneration == "" {
		w.liveGeneration = newSessionID()
	}
	return w.liveGeneration
}
func (w *Workspace) edited() { w.liveRevision++ }
