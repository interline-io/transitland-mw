package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/interline-io/log"
	"github.com/interline-io/transitland-mw/caches/rcache"
	"github.com/interline-io/transitland-mw/meters"
)

func init() {
	var _ meters.MeterProvider = &CacheMeterProvider{}
}

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
	users map[string]meters.MeterUser
}

// Wraps a meter with caching
type CacheMeterProvider struct {
	users *userPasser
	cache *rcache.Cache[CacheMeterKey, CacheMeterData]
	meters.MeterProvider
}

func NewCacheMeterProvider(provider meters.MeterProvider, topic string, redisClient *redis.Client, recheck time.Duration, expires time.Duration, refresh time.Duration) *CacheMeterProvider {
	// Horrible hack: pass user by string
	up := &userPasser{
		users: map[string]meters.MeterUser{},
	}

	// Refresh function
	refreshFn := func(ctx context.Context, key CacheMeterKey) (CacheMeterData, error) {
		log.TraceCheck(func() {
			log.Trace().Str("key", key.User).Msg("rechecking meter")
		})

		// Get user
		up.lock.Lock()
		user, ok := up.users[key.User]
		up.lock.Unlock()
		if !ok {
			return CacheMeterData{Value: 0}, errors.New("no user")
		}

		// Get value
		val, ok := provider.NewMeter(user).GetValue(
			key.MeterName,
			time.Unix(key.Start, 0),
			time.Unix(key.End, 0),
			nil,
		)
		log.TraceCheck(func() {
			log.Trace().Str("key", key.User).Float64("value", val).Bool("ok", ok).Msg("rechecking meter result")
		})
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

func (c *CacheMeterProvider) NewMeter(u meters.MeterUser) meters.ApiMeter {
	return &CacheMeter{
		user:     u,
		provider: c,
		ApiMeter: c.MeterProvider.NewMeter(u),
	}
}

func (m *CacheMeterProvider) getValue(u meters.MeterUser, meterName string, startTime time.Time, endTime time.Time, dims meters.Dimensions) (float64, bool) {
	if u == nil {
		return 0, false
	}

	// Horrible hack: pass user by string
	userName := u.ID()
	m.users.lock.Lock()
	m.users.users[userName] = u
	m.users.lock.Unlock()

	// Lookup in cache
	dbuf, _ := json.Marshal(dims)
	key := CacheMeterKey{
		User:      userName,
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
	user     meters.MeterUser
	provider *CacheMeterProvider
	meters.ApiMeter
}

func (m *CacheMeter) GetValue(meterName string, startTime time.Time, endTime time.Time, dims meters.Dimensions) (float64, bool) {
	return m.provider.getValue(m.user, meterName, startTime, endTime, dims)
}
