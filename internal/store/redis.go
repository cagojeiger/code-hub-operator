package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore reads last_used_at values from Redis. Values are stored as
// decimal Unix epoch seconds.
type RedisStore struct {
	client *redis.Client
}

// NewRedisStore wraps an existing Redis client.
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

// Get implements LastUsedStore.
func (r *RedisStore) Get(ctx context.Context, key string) (time.Time, bool, error) {
	raw, err := r.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("redis get %q: %w", key, err)
	}
	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("redis value for %q not int64 seconds: %w", key, err)
	}
	return time.Unix(secs, 0), true, nil
}
