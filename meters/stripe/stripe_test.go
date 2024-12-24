package stripe

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/interline-io/transitland-dbutil/testutil"
	"github.com/interline-io/transitland-mw/internal/metertest"
	"github.com/interline-io/transitland-mw/meters"
	stripemock "github.com/interline-io/transitland-mw/mocks"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/client"
	"go.uber.org/mock/gomock"
)

func TestStripeMeter(t *testing.T) {
	checkKeys := []string{
		"TL_TEST_STRIPE_APIKEY",
		"TL_TEST_STRIPE_METER1",
		"TL_TEST_STRIPE_METER2",
		"TL_TEST_STRIPE_USER1",
		"TL_TEST_STRIPE_USER2",
		"TL_TEST_STRIPE_USER3",
	}
	for _, k := range checkKeys {
		_, a, ok := testutil.CheckEnv(k)
		if !ok {
			t.Skip(errors.New(a))
			return
		}
	}

	eidKey := "stripe"
	testConfig := metertest.Config{
		TestMeter1: os.Getenv("TL_TEST_STRIPE_METER1"),
		TestMeter2: os.Getenv("TL_TEST_STRIPE_METER2"),
		User1:      metertest.NewTestUser(os.Getenv("TL_TEST_STRIPE_USER1"), map[string]string{eidKey: os.Getenv("TL_TEST_STRIPE_USER1")}),
		User2:      metertest.NewTestUser(os.Getenv("TL_TEST_STRIPE_USER2"), map[string]string{eidKey: os.Getenv("TL_TEST_STRIPE_USER2")}),
		User3:      metertest.NewTestUser(os.Getenv("TL_TEST_STRIPE_USER3"), map[string]string{eidKey: os.Getenv("TL_TEST_STRIPE_USER3")}),
	}

	mp := NewStripeMeterProvider(os.Getenv("TL_TEST_STRIPE_APIKEY"), 1*time.Second)
	mp.cfgs[testConfig.TestMeter1] = stripeConfig{Name: testConfig.TestMeter1, ExternalIDKey: eidKey}
	mp.cfgs[testConfig.TestMeter2] = stripeConfig{Name: testConfig.TestMeter2, ExternalIDKey: eidKey}
	metertest.TestMeter(t, mp, testConfig)
}

func TestStripeConfig(t *testing.T) {
	mp := NewStripeMeterProvider("test-key", 1*time.Second)

	t.Run("load config", func(t *testing.T) {
		testConfig := `{
			"test_meter": {
				"name": "test_meter",
				"default_user": "default-123",
				"external_id_key": "stripe_test",
				"dimensions": [
					{"key": "1", "value": "dim1"},
					{"key": "2", "value": "dim2"}
				]
			}
		}`
		tmpfile, err := os.CreateTemp("", "stripe-test-*.json")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpfile.Name())

		if _, err := tmpfile.Write([]byte(testConfig)); err != nil {
			t.Fatal(err)
		}
		if err := tmpfile.Close(); err != nil {
			t.Fatal(err)
		}

		if err := mp.LoadConfig(tmpfile.Name()); err != nil {
			t.Errorf("got error loading config: %v", err)
		}

		cfg, ok := mp.cfgs["test_meter"]
		if !ok {
			t.Error("config not loaded")
		}
		if cfg.Name != "test_meter" {
			t.Errorf("got name %q, want %q", cfg.Name, "test_meter")
		}
		if cfg.DefaultUser != "default-123" {
			t.Errorf("got default user %q, want %q", cfg.DefaultUser, "default-123")
		}
	})
}

func TestCustomerID(t *testing.T) {
	mp := NewStripeMeterProvider("test-key", 1*time.Second)
	cfg := stripeConfig{
		Name:          "test_meter",
		DefaultUser:   "default-123",
		ExternalIDKey: "stripe_test",
	}

	t.Run("default user", func(t *testing.T) {
		id, ok := mp.getCustomerID(cfg, nil)
		if !ok {
			t.Error("expected ok")
		}
		if id != "default-123" {
			t.Errorf("got id %q, want %q", id, "default-123")
		}
	})

	t.Run("user with external id", func(t *testing.T) {
		user := metertest.NewTestUser("test-user", map[string]string{"stripe_test": "user-123"})
		id, ok := mp.getCustomerID(cfg, user)
		if !ok {
			t.Error("expected ok")
		}
		if id != "user-123" {
			t.Errorf("got id %q, want %q", id, "user-123")
		}
	})

	t.Run("user without external id", func(t *testing.T) {
		user := metertest.NewTestUser("test-user", nil)
		id, ok := mp.getCustomerID(cfg, user)
		if !ok {
			t.Error("expected ok")
		}
		if id != "default-123" {
			t.Errorf("got id %q, want %q", id, "default-123")
		}
	})
}

func TestGetConfig(t *testing.T) {
	mp := NewStripeMeterProvider("test-key", 1*time.Second)
	mp.cfgs["test_meter"] = stripeConfig{
		Name: "test_meter",
	}

	t.Run("existing config", func(t *testing.T) {
		cfg, ok := mp.getcfg("test_meter")
		if !ok {
			t.Error("expected ok")
		}
		if cfg.Name != "test_meter" {
			t.Errorf("got name %q, want %q", cfg.Name, "test_meter")
		}
	})

	t.Run("missing config", func(t *testing.T) {
		cfg, ok := mp.getcfg("missing_meter")
		if !ok {
			t.Error("expected not ok")
		}
		if cfg.Name != "missing_meter" {
			t.Errorf("got name %q, want %q", cfg.Name, "missing_meter")
		}
	})
}

func TestStripeMeterWithMock(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockBackend := stripemock.NewMockBackend(mockCtrl)
	sc := client.New("test_key", &stripe.Backends{
		API:     mockBackend,
		Uploads: mockBackend,
	})

	mp := &StripeMeterProvider{
		client:   sc,
		interval: 1 * time.Second,
		apiKey:   "test_key",
		cfgs: map[string]stripeConfig{
			"test_meter": {
				Name:          "test_meter",
				DefaultUser:   "default-123",
				ExternalIDKey: "stripe_id",
				Dimensions: meters.Dimensions{
					{Key: "1", Value: "dimension1"},
				},
			},
		},
	}

	testUser := metertest.NewTestUser("test-user", map[string]string{
		"stripe_id": "customer-123",
	})

	t.Run("sendMeter", func(t *testing.T) {
		// Mock session creation response
		sessionResp := map[string]interface{}{
			"id":                   "mes_123",
			"authentication_token": "test_token",
			"expires_at":           time.Now().Add(time.Hour).Format(time.RFC3339),
		}
		sessionRespJSON, _ := json.Marshal(sessionResp)

		mockBackend.EXPECT().
			CallRaw(
				gomock.Any(),
				"POST",
				"/v2/billing/meter_event_session",
				gomock.Any(),
				gomock.Any(),
				gomock.Any(),
			).DoAndReturn(func(_ interface{}, _ interface{}, _ interface{}, _ interface{}, _ interface{}, v interface{}) error {
			resp := v.(*stripe.APIResponse)
			resp.RawJSON = sessionRespJSON
			resp.StatusCode = http.StatusOK
			return nil
		}).Times(1)

		mockBackend.EXPECT().
			CallRaw(
				gomock.Any(),
				"POST",
				"/v2/billing/meter_event_stream",
				gomock.Any(),
				gomock.Any(),
				gomock.Any(),
			).Return(nil).Times(1)

		err := mp.sendMeter(testUser, "test_meter", 100, meters.Dimensions{
			{Key: "2", Value: "extra_dimension"},
		})
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}

		// Wait for batch processing
		time.Sleep(2 * time.Second)
		mp.Flush() // Ensure any remaining events are processed
	})

	t.Run("GetValue", func(t *testing.T) {
		// TODO: Add tests once GetValue is implemented using Stripe's v2 metering API
		t.Skip("GetValue not yet implemented for Stripe v2 metering API")
	})
}
