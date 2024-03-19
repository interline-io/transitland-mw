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

	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	redis_store "github.com/eko/gocache/store/redis/v4"
	gocache "github.com/patrickmn/go-cache"
	redis9 "github.com/redis/go-redis/v9"
)

type cacheItem[T any] struct {
	Value     T
	RecheckAt time.Time
}

func (item cacheItem[T]) MarshalBinary() ([]byte, error) {
	return json.Marshal(&item)
}

func (item *cacheItem[T]) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, item)
}

type Cache[K comparable, T any] struct {
	RedisTimeout   time.Duration
	RefreshTimeout time.Duration
	Recheck        time.Duration
	Expires        time.Duration
	refreshFn      func(context.Context, K) (T, error)
	topic          string
	redisClient    *redis.Client
	loadCache      *cache.LoadableCache[cacheItem[T]]
	chainCache     *cache.ChainCache[cacheItem[T]]
	localCache     *cache.Cache[cacheItem[T]]
	redisCache     *cache.Cache[cacheItem[T]]
	lock           sync.Mutex
	localKeys      map[K]time.Time
}

func NewCache[K comparable, T any](refreshFn func(context.Context, K) (T, error), keyPrefix string, redisClient *redis.Client, recheckTtl time.Duration, expiresTtl time.Duration) *Cache[K, T] {
	// Setup obj
	rc := Cache[K, T]{
		refreshFn:      refreshFn,
		topic:          keyPrefix,
		redisClient:    redisClient,
		Recheck:        recheckTtl,
		Expires:        expiresTtl,
		RefreshTimeout: 1 * time.Second,
		localKeys:      map[K]time.Time{},
	}

	// In memory store
	gocacheStore := gocache_store.NewGoCache(
		gocache.New(rc.Expires, 0),
		store.WithExpiration(rc.Recheck),
	)

	// Redis store
	redisStore := redis_store.NewRedis(
		redis9.NewClient(&redis9.Options{Addr: "127.0.0.1:6379"}),
		store.WithExpiration(rc.Expires),
	)

	// Setup caches
	rc.localCache = cache.New[cacheItem[T]](gocacheStore)
	rc.redisCache = cache.New[cacheItem[T]](redisStore)
	rc.chainCache = cache.NewChain[cacheItem[T]](rc.localCache, rc.redisCache)
	loadFn := func(ctx context.Context, akey any) (cacheItem[T], error) {
		key, ok := akey.(K)
		if !ok {
			panic("failed to assert key")
		}
		ret, err := rc.Refresh(ctx, key)
		retItem := cacheItem[T]{
			Value:     ret,
			RecheckAt: time.Now().Add(rc.Recheck),
		}
		return retItem, err
	}
	rc.loadCache = cache.NewLoadable[cacheItem[T]](loadFn, rc.chainCache)
	rc.Start(rc.Recheck)
	return &rc
}

func (rc *Cache[K, T]) Start(t time.Duration) {
	if t <= 0 {
		return
	}
	ticker := time.NewTicker(t)
	go func() {
		for t := range ticker.C {
			_ = t
			ctx := context.Background()
			var keys []K
			var okKeys []K
			now := time.Now()
			rc.lock.Lock()
			for key, recheckAt := range rc.localKeys {
				if now.After(recheckAt) {
					keys = append(keys, key)
				} else {
					okKeys = append(okKeys, key)
				}
			}
			rc.lock.Unlock()
			log.Trace().Any("keys", keys).Any("okKeys", okKeys).Msg("auto refresh keys")
			for _, key := range keys {
				log.Trace().Str("key", toString(key)).Msg("auto refresh")
				ret, err := rc.Refresh(ctx, key)
				if err != nil {
					log.Error().Str("key", toString(key)).Msg("failed to auto refresh")
					continue
				}
				retItem := cacheItem[T]{Value: ret, RecheckAt: now.Add(rc.Recheck)}
				rc.chainCache.Set(ctx, key, retItem, store.WithExpiration(rc.Expires))
				rc.lock.Lock()
				rc.localKeys[key] = retItem.RecheckAt
				rc.lock.Unlock()
			}
		}
	}()
}

func (rc *Cache[K, T]) Check(ctx context.Context, key K) (T, bool) {
	// Check the chain cache without running loader
	log.Trace().Str("key", toString(key)).Msg("cache check")
	a, err := rc.chainCache.Get(ctx, key)
	if err != nil {
		return a.Value, false
	}
	rc.lock.Lock()
	rc.localKeys[key] = a.RecheckAt
	rc.lock.Unlock()
	return a.Value, true
}

func (rc *Cache[K, T]) Get(ctx context.Context, key K) (T, bool) {
	log.Trace().Str("key", toString(key)).Msg("cache get")
	a, err := rc.loadCache.Get(ctx, key)
	if err != nil {
		return a.Value, false
	}
	rc.lock.Lock()
	rc.localKeys[key] = a.RecheckAt
	rc.lock.Unlock()
	return a.Value, true
}

func (rc *Cache[K, T]) Refresh(ctx context.Context, key K) (T, error) {
	kstr := toString(key)
	log.Trace().Str("key", kstr).Msg("cache refresh: start")
	type rt struct {
		item T
		err  error
	}
	result := make(chan rt, 1)
	go func(ctx context.Context, key K) {
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
		log.Error().Err(err).Str("key", kstr).Msg("cache refresh: failed to refresh")
		return item, err
	}
	log.Trace().Str("key", kstr).Msg("cache refresh: ok")
	return item, nil
}

func (rc *Cache[K, T]) setRedis(ctx context.Context, key K, item cacheItem[T]) error {
	return rc.redisCache.Set(ctx, rc.redisKey(key), item)
}

func (rc *Cache[K, T]) redisKey(key K) string {
	kstr := toString(key)
	return fmt.Sprintf("rcache:%s:%s", rc.topic, kstr)
}

type canString interface {
	String() string
}

func toString(item any) string {
	if v, ok := item.(canString); ok {
		return v.String()
	}
	data, err := json.Marshal(item)
	if err != nil {
		panic(err)
	}
	return string(data)
}
