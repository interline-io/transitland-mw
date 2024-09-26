package amberflo

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/interline-io/transitland-dbutil/testutil"
	"github.com/interline-io/transitland-mw/internal/metertest"
)

func TestAmberfloMeter(t *testing.T) {
	checkKeys := []string{
		"TL_TEST_AMBERFLO_APIKEY",
		"TL_TEST_AMBERFLO_METER1",
		"TL_TEST_AMBERFLO_METER2",
		"TL_TEST_AMBERFLO_USER1",
		"TL_TEST_AMBERFLO_USER2",
		"TL_TEST_AMBERFLO_USER3",
	}
	for _, k := range checkKeys {
		_, a, ok := testutil.CheckEnv(k)
		if !ok {
			t.Skip(errors.New(a))
			return
		}
	}
	eidKey := "amberflo"
	testConfig := metertest.Config{
		TestMeter1: os.Getenv("TL_TEST_AMBERFLO_METER1"),
		TestMeter2: os.Getenv("TL_TEST_AMBERFLO_METER2"),
		User1:      metertest.NewTestUser(os.Getenv("TL_TEST_AMBERFLO_USER1"), map[string]string{eidKey: os.Getenv("TL_TEST_AMBERFLO_USER1")}),
		User2:      metertest.NewTestUser(os.Getenv("TL_TEST_AMBERFLO_USER2"), map[string]string{eidKey: os.Getenv("TL_TEST_AMBERFLO_USER2")}),
		User3:      metertest.NewTestUser(os.Getenv("TL_TEST_AMBERFLO_USER3"), map[string]string{eidKey: os.Getenv("TL_TEST_AMBERFLO_USER3")}),
	}
	mp := NewAmberfloMeterProvider(os.Getenv("TL_TEST_AMBERFLO_APIKEY"), 1*time.Second, 1)
	mp.cfgs[testConfig.TestMeter1] = amberFloConfig{Name: testConfig.TestMeter1, ExternalIDKey: eidKey}
	mp.cfgs[testConfig.TestMeter2] = amberFloConfig{Name: testConfig.TestMeter2, ExternalIDKey: eidKey}
	metertest.TestMeter(t, mp, testConfig)
}
