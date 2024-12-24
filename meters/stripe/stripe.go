package stripe

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"net/http"

	"github.com/interline-io/log"
	"github.com/interline-io/transitland-mw/meters"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/client"
	"github.com/stripe/stripe-go/v81/rawrequest"
)

func init() {
	var _ meters.MeterProvider = &StripeMeterProvider{}
}

// StripeMeterProvider implements the MeterProvider interface for Stripe's metering API.
// It handles batching of meter events and automatic session token refresh.
type StripeMeterProvider struct {
	client           *client.API
	interval         time.Duration
	cfgs             map[string]stripeConfig
	sessionAuthToken string
	sessionExpiresAt string
	apiKey           string
	batchEvents      []meterEvent
	batchMutex       sync.Mutex
}

type stripeConfig struct {
	Name          string            `json:"name,omitempty"`
	DefaultUser   string            `json:"default_user,omitempty"`
	ExternalIDKey string            `json:"external_id_key,omitempty"`
	Dimensions    meters.Dimensions `json:"dimensions,omitempty"`
}

type meterEvent struct {
	EventName  string
	CustomerId string
	Value      float64
	Dimensions meters.Dimensions
}

const maxBatchSize = 100

// NewStripeMeterProvider creates a new Stripe meter provider with the given API key
// and batch interval. It automatically starts a background worker to handle event batching.
func NewStripeMeterProvider(apiKey string, interval time.Duration) *StripeMeterProvider {
	config := &stripe.BackendConfig{
		EnableTelemetry: stripe.Bool(false),
	}
	sc := client.New(apiKey, &stripe.Backends{
		API: stripe.GetBackendWithConfig(stripe.APIBackend, config),
	})

	mp := &StripeMeterProvider{
		client:   sc,
		interval: interval,
		cfgs:     map[string]stripeConfig{},
		apiKey:   apiKey,
	}
	return mp
}

// LoadConfig loads meter configurations from a JSON file
func (m *StripeMeterProvider) LoadConfig(path string) error {
	cfgs := map[string]stripeConfig{}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &cfgs); err != nil {
		return err
	}
	m.cfgs = cfgs
	return nil
}

func (m *StripeMeterProvider) NewMeter(user meters.MeterUser) meters.ApiMeter {
	return &stripeMeter{
		user: user,
		mp:   m,
	}
}

func (m *StripeMeterProvider) Close() error {
	return nil
}

func (m *StripeMeterProvider) Flush() error {
	m.batchMutex.Lock()
	events := m.batchEvents
	m.batchEvents = nil
	m.batchMutex.Unlock()

	if len(events) == 0 {
		return nil
	}

	// Refresh session token if needed
	if err := m.refreshMeterEventSession(); err != nil {
		return fmt.Errorf("unable to refresh meter event session: %v", err)
	}

	// Get meter events backend with session token
	b, err := stripe.GetRawRequestBackend(stripe.APIBackend)
	if err != nil {
		return err
	}
	sessionClient := rawrequest.Client{B: b, Key: m.sessionAuthToken}

	// Convert events to API payload
	eventPayloads := make([]interface{}, len(events))
	for i, evt := range events {
		// Build payload with dimensions
		payload := map[string]interface{}{
			"stripe_customer_id": evt.CustomerId,
			"value":              fmt.Sprintf("%f", evt.Value),
		}
		// I think we can add extra "dimensions" to payload, but not sure
		for _, dim := range evt.Dimensions {
			payload[dim.Key] = dim.Value
		}

		eventPayloads[i] = map[string]interface{}{
			"event_name": evt.EventName,
			"payload":    payload,
		}
	}

	params := map[string]interface{}{
		"events": eventPayloads,
	}

	body, err := json.Marshal(params)
	if err != nil {
		return err
	}

	_, err = sessionClient.RawRequest(http.MethodPost, "/v2/billing/meter_event_stream", string(body), nil)
	return err
}

// sendMeter sends metering data to Stripe
// See Stripe's documentation for creating meter events: https://docs.stripe.com/api/v2/billing/meter-event-stream/create
func (m *StripeMeterProvider) sendMeter(user meters.MeterUser, meterName string, value float64, extraDimensions meters.Dimensions) error {
	cfg, ok := m.getcfg(meterName)
	if !ok {
		return nil
	}

	customerId, ok := m.getCustomerID(cfg, user)
	if !ok {
		log.Error().Str("user", user.ID()).Msg("could not meter; no stripe customer id")
		return nil
	}

	m.batchMutex.Lock()
	m.batchEvents = append(m.batchEvents, meterEvent{
		EventName:  meterName,
		CustomerId: customerId,
		Value:      value,
		Dimensions: extraDimensions,
	})
	shouldFlush := len(m.batchEvents) >= maxBatchSize
	m.batchMutex.Unlock()

	if shouldFlush {
		return m.Flush()
	}
	return nil
}

func (m *StripeMeterProvider) GetValue(user meters.MeterUser, meterName string, startTime time.Time, endTime time.Time, dims meters.Dimensions) (float64, bool) {
	// TODO: Implement GetValue using https://docs.stripe.com/api/billing/meter-event-summary
	// Not needed
	log.Error().Msg("GetValue not yet implemented")
	return 0, false
}

func (m *StripeMeterProvider) getCustomerID(cfg stripeConfig, user meters.MeterUser) (string, bool) {
	customerId := cfg.DefaultUser
	if user != nil {
		eidKey := cfg.ExternalIDKey
		if eidKey == "" {
			eidKey = "stripe"
		}
		if a, ok := user.GetExternalData(eidKey); ok {
			customerId = a
		}
	}
	if customerId == "" {
		log.Error().Str("user", user.ID()).Str("external_id_key", cfg.ExternalIDKey).Msg("could not get value; no stripe customer id")
	}
	return customerId, customerId != ""
}

func (m *StripeMeterProvider) getcfg(meterName string) (stripeConfig, bool) {
	cfg, ok := m.cfgs[meterName]
	if !ok {
		cfg = stripeConfig{
			Name: meterName,
		}
	}
	if cfg.Name == "" {
		log.Error().Str("meter", meterName).Msg("could not meter; no stripe config for meter")
		return cfg, false
	}
	return cfg, true
}

type stripeMeter struct {
	user meters.MeterUser
	mp   *StripeMeterProvider
}

func (m *stripeMeter) Meter(meterName string, value float64, extraDimensions meters.Dimensions) error {
	log.Trace().
		Str("user", m.user.ID()).
		Str("meter", meterName).
		Float64("meter_value", value).
		Msg("meter")
	return m.mp.sendMeter(m.user, meterName, value, extraDimensions)
}

func (m *stripeMeter) GetValue(meterName string, startTime time.Time, endTime time.Time, dims meters.Dimensions) (float64, bool) {
	return m.mp.GetValue(m.user, meterName, startTime, endTime, dims)
}

func (m *stripeMeter) AddDimension(meterName string, key string, value string) {
	// No-op: dimensions are handled directly in payload
}

// Add session refresh logic
// See Stripe's documentation for creating a meter event session: https://docs.stripe.com/api/v2/billing/meter-event-stream/session/create
func (m *StripeMeterProvider) refreshMeterEventSession() error {
	currentTime := time.Now().Format(time.RFC3339)

	// Check if session is null or expired
	if m.sessionAuthToken == "" || m.sessionExpiresAt <= currentTime {
		b, err := stripe.GetRawRequestBackend(stripe.APIBackend)
		if err != nil {
			return fmt.Errorf("failed to get stripe backend: %w", err)
		}
		client := rawrequest.Client{B: b, Key: m.apiKey}

		// Create a new meter event session
		rawResp, err := client.RawRequest(http.MethodPost, "/v2/billing/meter_event_session", "", nil)
		if err != nil {
			return fmt.Errorf("failed to create meter event session: %w", err)
		}
		if rawResp.StatusCode != http.StatusOK {
			return fmt.Errorf("meter event session request failed with status %d: %s", rawResp.StatusCode, rawResp.Status)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(rawResp.RawJSON, &resp); err != nil {
			return fmt.Errorf("failed to unmarshal session response: %w", err)
		}

		authToken, ok := resp["authentication_token"].(string)
		if !ok {
			return fmt.Errorf("missing or invalid authentication_token in response")
		}
		expiresAt, ok := resp["expires_at"].(string)
		if !ok {
			return fmt.Errorf("missing or invalid expires_at in response")
		}

		m.sessionAuthToken = authToken
		m.sessionExpiresAt = expiresAt
	}
	return nil
}
