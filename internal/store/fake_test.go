package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFakeStore_EmptyReturnsNotFound(t *testing.T) {
	fs := NewFakeStore()
	ts, ok, err := fs.Get(context.Background(), "missing")
	require.NoError(t, err)
	require.False(t, ok)
	require.True(t, ts.IsZero())
}

func TestFakeStore_SetGetRoundTrip(t *testing.T) {
	fs := NewFakeStore()
	want := time.Unix(1712839200, 0)
	fs.Set("k", want)

	got, ok, err := fs.Get(context.Background(), "k")
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, want.Equal(got))
}

func TestFakeStore_Delete(t *testing.T) {
	fs := NewFakeStore()
	fs.Set("k", time.Now())
	fs.Delete("k")

	_, ok, err := fs.Get(context.Background(), "k")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestFakeStore_ErrorPropagates(t *testing.T) {
	fs := NewFakeStore()
	wantErr := errors.New("boom")
	fs.SetError(wantErr)

	_, _, err := fs.Get(context.Background(), "k")
	require.ErrorIs(t, err, wantErr)
}

func TestFakeStore_ErrorClears(t *testing.T) {
	fs := NewFakeStore()
	fs.SetError(errors.New("boom"))
	fs.SetError(nil)

	// Should no longer error.
	_, ok, err := fs.Get(context.Background(), "k")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestFakeStore_ConcurrentAccess(t *testing.T) {
	// Smoke test for the RWMutex: hammer Set/Get from many goroutines.
	fs := NewFakeStore()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			fs.Set("k", time.Now())
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		_, _, _ = fs.Get(context.Background(), "k")
	}
	<-done
}
