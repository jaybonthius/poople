package app

import "sync"

// Topic hands out per-key change channels. A channel closes after the next
// committed change to its key; a view takes the channel before reading SQLite
// so a commit between the read and the wait is never missed.
type Topic struct {
	mu      sync.Mutex
	changed map[int64]chan struct{}
}

// Changes closes after the next Notify for key.
func (t *Topic) Changes(key int64) <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.changed == nil {
		t.changed = make(map[int64]chan struct{})
	}
	ch, ok := t.changed[key]
	if !ok {
		ch = make(chan struct{})
		t.changed[key] = ch
	}
	return ch
}

// Notify wakes every view of key. Call it only after the change commits.
func (t *Topic) Notify(key int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ch, ok := t.changed[key]; ok {
		close(ch)
		delete(t.changed, key)
	}
}
