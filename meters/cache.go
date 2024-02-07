package meters

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/interline-io/log"
	"github.com/interline-io/transitland-mw/rcache"
)

// CacheMeterKey should map to GetValue arguments
type CacheMeterKey struct {
	User      string
	MeterName string
	Start     int64
	End       int64
	Dims      string
}

func (c CacheMeterKey) String() string {
	return fmt.Sprintf(
		"%s:%s:%d:%d:%s",
		c.User,
		c.MeterName,
		c.Start,
		c.End,
		c.Dims,
	)
}

// A simple value
type CacheMeterData struct {
	Value float64
}

// Horrible hack to pass users
type userPasser struct {
	lock  sync.Mutex
	users map[string]MeterUser
}

// Wraps a meter with caching
type CacheMeterProvider struct {
	users *userPasser
	cache *rcache.Cache[CacheMeterKey, CacheMeterData]
	MeterProvider
}

func NewCacheMeterProvider(provider MeterProvider, topic string, redisClient *redis.Client, recheck time.Duration, expires time.Duration, refresh time.Duration) *CacheMeterProvider {
	// Horrible hack: pass user by string
	up := &userPasser{
		users: map[string]MeterUser{},
	}

	// Refresh function
	refreshFn := func(ctx context.Context, key CacheMeterKey) (CacheMeterData, error) {
		log.Info().Str("key", key.User).Msg("rechecking meter")

		// Get user
		up.lock.Lock()
		user, ok := up.users[key.User]
		up.lock.Unlock()
		if !ok {
			panic("no user")
		}

		// Get value
		val, ok := provider.GetValue(
			user,
			key.MeterName,
			time.Unix(key.Start, 0),
			time.Unix(key.End, 0),
			nil,
		)
		log.Info().Str("key", key.User).Float64("value", val).Bool("ok", ok).Msg("rechecking meter result")

		// Clear user
		up.lock.Lock()
		delete(up.users, key.User)
		up.lock.Unlock()

		return CacheMeterData{Value: val}, nil
	}

	// Create cache
	cache := rcache.NewCache[CacheMeterKey, CacheMeterData](
		refreshFn,
		topic,
		redisClient,
	)
	cache.Expires = expires
	cache.Recheck = recheck
	cache.Start(refresh)
	return &CacheMeterProvider{
		MeterProvider: provider,
		users:         up,
		cache:         cache,
	}
}

func (c *CacheMeterProvider) NewMeter(u MeterUser) ApiMeter {
	return &CacheMeter{
		user:     u,
		provider: c,
		ApiMeter: c.MeterProvider.NewMeter(u),
	}
}

func (m *CacheMeterProvider) GetValue(user MeterUser, meterName string, startTime time.Time, endTime time.Time, dims Dimensions) (float64, bool) {
	// Horrible hack: pass user by string
	m.users.lock.Lock()
	m.users.users[user.ID()] = user
	m.users.lock.Unlock()

	// Lookup in cache
	dbuf, _ := json.Marshal(dims)
	key := CacheMeterKey{
		User:      user.ID(),
		MeterName: meterName,
		Start:     startTime.Unix(),
		End:       endTime.Unix(),
		Dims:      string(dbuf),
	}
	if a, ok := m.cache.Get(context.Background(), key); ok {
		return a.Value, true
	}
	return 0, false
}

type CacheMeter struct {
	user     MeterUser
	provider *CacheMeterProvider
	ApiMeter
}

func (m *CacheMeter) GetValue(meterName string, startTime time.Time, endTime time.Time, dims Dimensions) (float64, bool) {
	return m.provider.GetValue(m.user, meterName, startTime, endTime, dims)
}
