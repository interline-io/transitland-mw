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

// StripeMeterProvider implements the MeterProvider interface for Stripe's metering API
type StripeMeterProvider struct {
	client           *client.API
	interval         time.Duration
	cfgs             map[string]stripeConfig
	sessionAuthToken string
	sessionExpiresAt string
	apiKey           string
	batchEvents      []meterEvent
	batchMutex       sync.Mutex
	eventChan        chan meterEvent
	done             chan struct{}
}

type stripeConfig struct {
	Name          string            `json:"name,omitempty"`
	DefaultUser   string            `json:"default_user,omitempty"`
	ExternalIDKey string            `json:"external_id_key,omitempty"`
	Dimensions    meters.Dimensions `json:"dimensions,omitempty"`
}

// NewStripeMeterProvider creates a new Stripe meter provider
func NewStripeMeterProvider(apiKey string, interval time.Duration) *StripeMeterProvider {
	config := &stripe.BackendConfig{
		EnableTelemetry: stripe.Bool(false),
	}
	sc := client.New(apiKey, &stripe.Backends{
		API: stripe.GetBackendWithConfig(stripe.APIBackend, config),
	})

	mp := &StripeMeterProvider{
		client:    sc,
		interval:  interval,
		cfgs:      map[string]stripeConfig{},
		apiKey:    apiKey,
		eventChan: make(chan meterEvent, maxBatchSize),
		done:      make(chan struct{}),
	}
	go mp.batchWorker()
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
	close(m.done)
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
	b, err := stripe.GetRawRequestBackend(stripe.MeterEventsBackend)
	if err != nil {
		return err
	}
	sessionClient := rawrequest.Client{B: b, Key: m.sessionAuthToken}

	// Convert events to API payload
	eventPayloads := make([]interface{}, len(events))
	for i, evt := range events {
		eventPayloads[i] = map[string]interface{}{
			"event_name": evt.EventName,
			"payload": map[string]interface{}{
				"stripe_customer_id": evt.CustomerId,
				"value":              fmt.Sprintf("%f", evt.Value),
				"metadata":           buildMetadataFromDimensions(nil, evt.Dimensions),
			},
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

func buildMetadataFromDimensions(cfgDims, extraDims meters.Dimensions) map[string]string {
	metadata := make(map[string]string)
	for _, d := range cfgDims {
		metadata[d.Key] = d.Value
	}
	for _, d := range extraDims {
		metadata[d.Key] = d.Value
	}
	return metadata
}

func (m *StripeMeterProvider) GetValue(user meters.MeterUser, meterName string, startTime time.Time, endTime time.Time, dims meters.Dimensions) (float64, bool) {
	cfg, ok := m.getcfg(meterName)
	if !ok {
		return 0, false
	}

	customerId, ok := m.getCustomerID(cfg, user)
	if !ok {
		log.Error().Str("user", user.ID()).Msg("could not get value; no stripe customer id")
		return 0, false
	}

	params := &stripe.UsageRecordSummaryListParams{
		SubscriptionItem: stripe.String(customerId),
	}

	iter := m.client.UsageRecordSummaries.List(params)
	var total float64
	for iter.Next() {
		summary := iter.UsageRecordSummary()
		if summary.Period.Start >= startTime.Unix() && summary.Period.Start <= endTime.Unix() {
			total += float64(summary.TotalUsage)
		}
	}
	if err := iter.Err(); err != nil {
		log.Error().Err(err).Msg("could not get usage summary")
		return 0, false
	}

	return total, true
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
			return err
		}
		client := rawrequest.Client{B: b, Key: m.apiKey}

		// Create a new meter event session
		rawResp, err := client.RawRequest(http.MethodPost, "/v2/billing/meter_event_session", "", nil)
		if err != nil {
			return err
		}
		if rawResp.StatusCode != 200 {
			return fmt.Errorf("meter event session request failed: %s", rawResp.Status)
		}

		var resp map[string]interface{}
		err = json.Unmarshal(rawResp.RawJSON, &resp)
		if err != nil {
			return err
		}

		m.sessionAuthToken = resp["authentication_token"].(string)
		m.sessionExpiresAt = resp["expires_at"].(string)
	}
	return nil
}

func (m *StripeMeterProvider) batchWorker() {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	var batch []meterEvent
	for {
		select {
		case evt := <-m.eventChan:
			batch = append(batch, evt)
			if len(batch) >= maxBatchSize {
				m.sendBatch(batch)
				batch = nil
			}
		case <-ticker.C:
			if len(batch) > 0 {
				m.sendBatch(batch)
				batch = nil
			}
		case <-m.done:
			if len(batch) > 0 {
				m.sendBatch(batch)
			}
			return
		}
	}
}
