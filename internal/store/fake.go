package store

import (
	"context"
	"sync"
	"time"
)

// FakeStore is an in-memory LastUsedStore implementation intended for tests.
type FakeStore struct {
	mu      sync.RWMutex
	entries map[string]time.Time
	err     error
}

// NewFakeStore returns an empty FakeStore.
func NewFakeStore() *FakeStore {
	return &FakeStore{entries: make(map[string]time.Time)}
}

// Set records ts under key.
func (f *FakeStore) Set(key string, ts time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[key] = ts
}

// Delete removes key from the store.
func (f *FakeStore) Delete(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, key)
}

// SetError makes subsequent Get calls return err. Pass nil to clear.
func (f *FakeStore) SetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Get implements LastUsedStore.
func (f *FakeStore) Get(_ context.Context, key string) (time.Time, bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.err != nil {
		return time.Time{}, false, f.err
	}
	ts, ok := f.entries[key]
	if !ok {
		return time.Time{}, false, nil
	}
	return ts, true, nil
}
