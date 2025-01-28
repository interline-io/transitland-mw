package cache

import (
	"fmt"
	"testing"
	"time"

	"github.com/interline-io/transitland-dbutil/testutil"
	"github.com/interline-io/transitland-mw/meters"
	limitmeter "github.com/interline-io/transitland-mw/meters/limit"
	localmeter "github.com/interline-io/transitland-mw/meters/local"
	"github.com/interline-io/transitland-mw/meters/metertest"
	"github.com/stretchr/testify/assert"
)

func TestCacheMeter(t *testing.T) {
	// redis jobs and cache
	if a, ok := testutil.CheckTestRedisClient(); !ok {
		t.Skip(a)
		return
	}
	redisClient := testutil.MustOpenTestRedisClient(t)

	t1, t2, err := meters.PeriodSpan("monthly")
	if err != nil {
		t.Fatal(err)
	}

	user := metertest.NewTestUser("test1", nil)
	meterName := "ok"
	mp := localmeter.NewLocalMeterProvider()
	cmp := NewCacheMeterProvider(
		mp,
		"testcachemeter",
		redisClient,
		2*time.Second,
		1*time.Hour,
		4*time.Second,
	)
	cmpm := cmp.NewMeter(user)
	lastValue := 0.0
	for i := 0; i < 10; i++ {
		val, ok := cmpm.GetValue(meterName, t1, t2, nil)
		_ = ok
		lastValue = val
		if err := cmpm.Meter(meterName, 1, nil); err != nil {
			t.Error(err)
		}
		time.Sleep(1 * time.Second)
	}
	assert.Equal(t, 8.0, lastValue)
	finalVal, _ := mp.NewMeter(user).GetValue(meterName, t1, t2, nil)
	assert.Equal(t, 10.0, finalVal)
}

func TestCacheMeter_Limits(t *testing.T) {
	if a, ok := testutil.CheckTestRedisClient(); !ok {
		t.Skip(a)
		return
	}
	redisClient := testutil.MustOpenTestRedisClient(t)
	meterName := "testmeter"
	testKey := 1
	lim := limitmeter.UserMeterLimit{
		MeterName: meterName,
		Period:    "hourly",
		Limit:     5.0,
		Dims:      meters.Dimensions{{Key: "ok", Value: fmt.Sprintf("foo:%d", testKey)}},
	}
	user := metertest.NewTestUser("testuser", nil)
	baseMp := localmeter.NewLocalMeterProvider()
	cacheMp := NewCacheMeterProvider(
		baseMp,
		"testcachemeter",
		redisClient,
		2000*time.Millisecond,
		1*time.Hour,
		4000*time.Millisecond,
	)
	limitMp := limitmeter.NewLimitMeterProvider(cacheMp)
	limitMp.Enabled = true
	limitMp.DefaultLimits = []limitmeter.UserMeterLimit{lim}
	m := limitMp.NewMeter(user)
	for i := 0; i < 10; i++ {
		if ok, err := m.Check(meterName, 1.0, lim.Dims); ok {
			if err := m.Meter(meterName, 1.0, lim.Dims); err != nil {
				t.Error(err)
			}
		} else if err != nil {
			t.Error(err)
		}
		time.Sleep(1 * time.Second)

	}
	// Final value should be between 5 and less than 10
	t1, t2 := lim.Span()
	finalVal, _ := m.GetValue(meterName, t1, t2, lim.Dims)
	assert.GreaterOrEqual(t, finalVal, 5.0, "expected >=5")
	assert.Less(t, finalVal, 10.0, "expected <10")
}
