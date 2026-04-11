// Package store abstracts the "last used at" external store used by the
// CodeHubRuntime controller to decide idle-vs-active state.
package store

import (
	"context"
	"time"
)

// LastUsedStore returns the last-used timestamp for a key. Implementations
// must be safe for concurrent use.
type LastUsedStore interface {
	// Get returns (ts, true, nil) when the key has a value, (zero, false, nil)
	// when the key is absent, and (zero, false, err) on transport or parse errors.
	Get(ctx context.Context, key string) (time.Time, bool, error)
}
