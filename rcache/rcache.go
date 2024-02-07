package rcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/interline-io/log"
)

type Item[T any] struct {
	Value     T
	ExpiresAt time.Time
	RecheckAt time.Time
}

type RefreshCache[T any] struct {
	RedisTimeout   time.Duration
	RefreshTimeout time.Duration
	Recheck        time.Duration
	Expires        time.Duration
	refreshFn      func(context.Context, string) (T, error)
	topic          string
	items          map[string]Item[T]
	lock           sync.Mutex
	redisClient    *redis.Client
}

func NewRefreshCache[T any](refreshFn func(context.Context, string) (T, error), keyPrefix string, redisClient *redis.Client) *RefreshCache[T] {
	topic := "test"
	rc := RefreshCache[T]{
		refreshFn:      refreshFn,
		topic:          topic,
		redisClient:    redisClient,
		items:          map[string]Item[T]{},
		Recheck:        1 * time.Hour,
		Expires:        1 * time.Hour,
		RefreshTimeout: 1 * time.Second,
		RedisTimeout:   1 * time.Second,
	}
	return &rc
}

func (rc *RefreshCache[T]) Start(t time.Duration) {
	ticker := time.NewTicker(t)
	go func() {
		for t := range ticker.C {
			_ = t
			ctx := context.Background()
			keys := rc.GetRecheckKeys(ctx)
			for _, key := range keys {
				rc.Refresh(ctx, key)
			}
		}
	}()
}

func (rc *RefreshCache[T]) Check(ctx context.Context, key string) (T, bool) {
	rc.lock.Lock()
	defer rc.lock.Unlock()
	return rc.check(ctx, key)
}

func (rc *RefreshCache[T]) check(ctx context.Context, key string) (T, bool) {
	a, ok := rc.getLocal(key)
	if ok {
		return a.Value, ok
	}
	b, ok := rc.getRedis(ctx, key)
	if ok {
		rc.setLocal(key, b)
	}
	return b.Value, ok
}

func (rc *RefreshCache[T]) Get(ctx context.Context, key string) (T, bool) {
	rc.lock.Lock()
	defer rc.lock.Unlock()
	a, ok := rc.check(ctx, key)
	if !ok {
		if val, err := rc.refresh(ctx, key); err == nil {
			a = val
			ok = true
		}
	}
	return a, ok
}

func (rc *RefreshCache[T]) SetTTL(ctx context.Context, key string, value T, ttl1 time.Duration, ttl2 time.Duration) error {
	rc.lock.Lock()
	defer rc.lock.Unlock()
	return rc.setTTL(ctx, key, value, ttl1, ttl2)
}

func (rc *RefreshCache[T]) setTTL(ctx context.Context, key string, value T, ttl1 time.Duration, ttl2 time.Duration) error {
	n := time.Now().In(time.UTC)
	item := Item[T]{
		Value:     value,
		RecheckAt: n.Add(ttl1),
		ExpiresAt: n.Add(ttl2),
	}
	rc.setLocal(key, item)
	rc.setRedis(ctx, key, item)
	return nil
}

func (rc *RefreshCache[T]) Refresh(ctx context.Context, key string) (T, error) {
	rc.lock.Lock()
	defer rc.lock.Unlock()
	return rc.refresh(ctx, key)
}

func (rc *RefreshCache[T]) refresh(ctx context.Context, key string) (T, error) {
	type rt struct {
		item T
		err  error
	}
	result := make(chan rt, 1)
	go func(ctx context.Context, key string) {
		item, err := rc.refreshFn(ctx, key)
		result <- rt{item: item, err: err}
	}(ctx, key)
	var err error
	var item T
	select {
	case <-time.After(rc.RefreshTimeout):
		err = errors.New("timed out")
	case ret := <-result:
		err = ret.err
		item = ret.item
	}
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("refresh: failed to refresh")
		return item, err
	}
	err = rc.setTTL(ctx, key, item, rc.Recheck, rc.Expires)
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("refresh: failed to set TTL")
		return item, err
	}
	log.Trace().Str("key", key).Msg("refresh: ok")
	return item, nil
}

func (rc *RefreshCache[T]) getLocal(key string) (Item[T], bool) {
	log.Trace().Str("key", key).Msg("local read: start")
	a, ok := rc.items[key]
	if !ok {
		log.Trace().Str("key", key).Msg("local read: not present")
		return a, false
	}
	if a.ExpiresAt.Before(time.Now()) {
		log.Trace().Str("key", key).Msg("local read: expired")
		return a, false
	}
	log.Trace().Str("key", key).Msg("local read: ok")
	return a, ok
}

func (rc *RefreshCache[T]) getRedis(ctx context.Context, key string) (Item[T], bool) {
	ekey := rc.redisKey(key)
	log.Trace().Str("key", ekey).Msg("redis read: start")
	if rc.redisClient == nil {
		log.Trace().Str("key", ekey).Msg("redis read: no redis client")
		return Item[T]{}, false
	}
	rctx, cc := context.WithTimeout(ctx, rc.RedisTimeout)
	defer cc()
	lastData := rc.redisClient.Get(rctx, ekey)
	if err := lastData.Err(); err != nil {
		log.Trace().Err(err).Str("key", ekey).Msg("redis read: not present")
		return Item[T]{}, false
	}
	a, err := lastData.Bytes()
	if err != nil {
		log.Error().Err(err).Str("key", ekey).Msg("redis read: bytes failed")
		return Item[T]{}, false
	}
	t := time.Now().In(time.UTC)
	ld := Item[T]{
		ExpiresAt: t,
		RecheckAt: t,
	}
	if err := json.Unmarshal(a, &ld); err != nil {
		log.Error().Err(err).Str("key", ekey).Msg("redis read: failed during unmarshal")
	}
	if ld.ExpiresAt.Before(time.Now()) {
		log.Trace().Str("key", ekey).Msg("redis read: expired")
		return ld, false
	}
	log.Trace().Str("key", ekey).Msg("redis read: ok")
	return ld, true
}

func (rc *RefreshCache[T]) setLocal(key string, item Item[T]) error {
	log.Trace().Str("key", key).Msg("local write: ok")
	rc.items[key] = item
	return nil
}

func (rc *RefreshCache[T]) setRedis(ctx context.Context, key string, item Item[T]) error {
	ekey := rc.redisKey(key)
	log.Trace().Str("key", ekey).Msg("redis write: start")
	if rc.redisClient == nil {
		log.Trace().Str("key", ekey).Msg("redis write: no redis client")
		return nil
	}
	rctx, cc := context.WithTimeout(ctx, rc.RedisTimeout)
	defer cc()
	data, err := json.Marshal(item)
	if err != nil {
		log.Error().Err(err).Str("key", ekey).Msg("redis write: failed during marshal")
		return err
	}
	log.Trace().Str("key", ekey).Str("data", string(data)).Msg("redis write: data")
	if err := rc.redisClient.Set(rctx, ekey, data, rc.Expires).Err(); err != nil {
		log.Error().Err(err).Str("key", ekey).Msg("redis write: failed")
	}
	log.Trace().Str("key", ekey).Msg("redis write: ok")
	return nil
}

func (rc *RefreshCache[T]) redisKey(key string) string {
	return fmt.Sprintf("ecache:%s:%s", rc.topic, key)
}

func (rc *RefreshCache[T]) GetRecheckKeys(ctx context.Context) []string {
	rc.lock.Lock()
	defer rc.lock.Unlock()
	t := time.Now().In(time.UTC)
	var ret []string
	for k, v := range rc.items {
		// Update?
		if v.RecheckAt.After(t) {
			continue
		}
		// Refresh local cache from redis
		if a, ok := rc.getRedis(ctx, k); ok {
			v = a
			rc.items[k] = v
		}
		// Check again
		if v.RecheckAt.After(t) {
			continue
		}
		ret = append(ret, k)
	}
	return ret
}
