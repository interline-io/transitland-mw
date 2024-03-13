package rcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type TestItem = Item[string]

type Item[T any] struct {
	Value     T
	ExpiresAt time.Time
	RecheckAt time.Time
}

func (item Item[T]) MarshalBinary() ([]byte, error) {
	return json.Marshal(&item)
}

func (item *Item[T]) UnmarshalBinary(data []byte) error {
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
	loadCache      *cache.LoadableCache[T]
	chainCache     *cache.ChainCache[T]
	localCache     *cache.Cache[T]
	redisCache     *cache.Cache[T]
	// items          map[K]Item[T]
	// lock         sync.Mutex
}

func NewCache[K comparable, T any](refreshFn func(context.Context, K) (T, error), keyPrefix string, redisClient *redis.Client, ttl time.Duration) *Cache[K, T] {
	// Setup obj
	rc := Cache[K, T]{
		refreshFn:      refreshFn,
		topic:          keyPrefix,
		redisClient:    redisClient,
		Expires:        ttl,
		RefreshTimeout: 1 * time.Second,
	}

	// In memory store
	gocacheStore := gocache_store.NewGoCache(
		gocache.New(rc.Expires, 0),
		store.WithExpiration(rc.Expires),
	)

	// Redis store
	redisStore := redis_store.NewRedis(
		redis9.NewClient(&redis9.Options{Addr: "127.0.0.1:6379"}),
		store.WithExpiration(rc.Expires),
	)

	// Setup caches
	rc.localCache = cache.New[T](gocacheStore)
	rc.redisCache = cache.New[T](redisStore)
	rc.chainCache = cache.NewChain[T](rc.localCache, rc.redisCache)
	loadFn := func(ctx context.Context, akey any) (T, error) {
		key, ok := akey.(K)
		if !ok {
			panic("failed to assert key")
		}
		fmt.Println("calling loader:", key)
		ret, err := rc.Refresh(ctx, key)
		fmt.Println("\t\tgot:", ret, "err:", err)
		return ret, err
	}
	rc.loadCache = cache.NewLoadable[T](loadFn, rc.chainCache)
	return &rc
}

func (rc *Cache[K, T]) Start(t time.Duration) {
}

func (rc *Cache[K, T]) Check(ctx context.Context, key K) (T, bool) {
	// Check the chain cache without running loader
	a, err := rc.chainCache.Get(ctx, key)
	if err != nil {
		return a, false
	}
	return a, true
}

func (rc *Cache[K, T]) Get(ctx context.Context, key K) (T, bool) {
	a, err := rc.loadCache.Get(ctx, key)
	if err != nil {
		return a, false
	}
	return a, true
}

func (rc *Cache[K, T]) Refresh(ctx context.Context, key K) (T, error) {
	kstr := toString(key)
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
		log.Error().Err(err).Str("key", kstr).Msg("refresh: failed to refresh")
		return item, err
	}
	log.Trace().Str("key", kstr).Msg("refresh: ok")
	return item, nil
}

func (rc *Cache[K, T]) setRedis(ctx context.Context, key K, item Item[T]) error {
	return rc.redisCache.Set(ctx, rc.redisKey(key), item.Value)
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
