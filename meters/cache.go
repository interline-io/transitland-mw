package meters

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
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

// Wraps a meter with caching
type CacheMeterProvider struct {
	cache *rcache.Cache[CacheMeterKey, CacheMeterData]
	MeterProvider
}

func NewCacheMeterProvider(provider MeterProvider, topic string, redisClient *redis.Client, recheck time.Duration, expires time.Duration, refresh time.Duration) *CacheMeterProvider {
	refreshFn := func(ctx context.Context, key CacheMeterKey) (CacheMeterData, error) {
		fmt.Println("recheck:", key)
		val, ok := provider.GetValue(
			nil,
			key.MeterName,
			time.Unix(key.Start, 0),
			time.Unix(key.End, 0),
			nil,
		)
		fmt.Println("recheck result:", val, ok)
		return CacheMeterData{Value: val}, nil
	}
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
