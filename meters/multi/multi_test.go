package multi

import (
	"context"
	"testing"

	"github.com/interline-io/transitland-mw/meters"
	"github.com/interline-io/transitland-mw/meters/local"
	"github.com/interline-io/transitland-mw/meters/metertest"
)

func TestMultiMeter(t *testing.T) {
	ctx := context.Background()
	testConfig := metertest.Config{
		TestMeter1: "test1",
		TestMeter2: "test2",
		User1:      metertest.NewTestUser("test1", nil),
		User2:      metertest.NewTestUser("test2", nil),
		User3:      metertest.NewTestUser("test3", nil),
	}

	u1m1 := 75.0
	u2m1 := 50.0

	mp1 := local.NewLocalMeterProvider()
	mp1m := mp1.NewMeter(testConfig.User1)
	if err := mp1m.Meter(ctx, meters.NewMeterEvent(testConfig.TestMeter1, u1m1, nil)); err != nil {
		t.Fatal(err)
	}

	mp2 := local.NewLocalMeterProvider()
	mp2m := mp2.NewMeter(testConfig.User2)
	if err := mp2m.Meter(ctx, meters.NewMeterEvent(testConfig.TestMeter1, u2m1, nil)); err != nil {
		t.Fatal(err)
	}

	mp := &MultiMeterProvider{
		meters: []meters.MeterProvider{mp1, mp2},
	}
	metertest.TestMeter(t, mp, testConfig)
	if err := mp.Flush(); err != nil {
		t.Fatal(err)
	}

	// Run test again to get baselines
	d1, d2, _ := meters.PeriodSpan("hourly")
	checkMeter := local.NewLocalMeterProvider()
	metertest.TestMeter(t, checkMeter, testConfig)
	u1m1base, _ := checkMeter.NewMeter(testConfig.User1).GetValue(ctx, testConfig.TestMeter1, d1, d2, nil)
	u2m1base, _ := checkMeter.NewMeter(testConfig.User2).GetValue(ctx, testConfig.TestMeter1, d1, d2, nil)
	if u1m1base <= 0 || u2m1base <= 2 {
		t.Fatalf("failed to get baseline meters, u1m1base: %f, u2m1base: %f", u1m1base, u2m1base)
	}

	// Test that the two meters are separate
	// Check that first meter includes the initial state + change
	u1m1expect := u1m1 + u1m1base
	if v, ok := mp1.NewMeter(testConfig.User1).GetValue(ctx, testConfig.TestMeter1, d1, d2, nil); !ok || v != u1m1expect {
		t.Fatalf("u1m1: expected %f, got %f", u1m1expect, v)
	}

	// Check that the second meter includes the initial state + change
	u2m1expect := u2m1 + u2m1base
	if v, ok := mp2.NewMeter(testConfig.User2).GetValue(ctx, testConfig.TestMeter1, d1, d2, nil); !ok || v != u2m1expect {
		t.Fatalf("u2m1: expected %f, got %f", u2m1expect, v)
	}
}
