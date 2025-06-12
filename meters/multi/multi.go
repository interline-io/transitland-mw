package multi

import (
	"context"
	"time"

	"github.com/interline-io/transitland-mw/meters"
)

func init() {
	var _ meters.MeterProvider = &MultiMeterProvider{}
}

type MultiMeterProvider struct {
	meters []meters.MeterProvider
}

func NewMultiMeterProvider(meters ...meters.MeterProvider) *MultiMeterProvider {
	return &MultiMeterProvider{
		meters: meters,
	}
}

func (m *MultiMeterProvider) NewMeter(user meters.MeterUser) meters.Meterer {
	var mets []meters.MeterRecorder
	for _, m := range m.meters {
		mets = append(mets, m.NewMeter(user))
	}
	return &multiMeterUser{
		mets: mets,
	}
}

func (m *MultiMeterProvider) Flush() error {
	for _, m := range m.meters {
		if err := m.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func (m *MultiMeterProvider) Close() error {
	for _, m := range m.meters {
		if err := m.Close(); err != nil {
			return err
		}
	}
	return nil
}

type multiMeterUser struct {
	mets []meters.MeterRecorder
}

func (m *multiMeterUser) Meter(ctx context.Context, meterEvent meters.MeterEvent) error {
	for _, m := range m.mets {
		if err := m.Meter(ctx, meterEvent); err != nil {
			return err
		}
	}
	return nil
}

func (m *multiMeterUser) WithDimension(key string, value string) meters.MeterRecorder {
	m2 := &multiMeterUser{
		mets: make([]meters.MeterRecorder, len(m.mets)),
	}
	for i := range m.mets {
		m2.mets = append(m2.mets, m.mets[i].WithDimension(key, value))
	}
	return m2
}

func (m *multiMeterUser) GetValue(ctx context.Context, meterName string, startTime time.Time, endTime time.Time, dims meters.Dimensions) (float64, bool) {
	for _, m := range m.mets {
		m2, ok := m.(meters.MeterReader)
		if !ok {
			continue // Skip if the meter does not implement MeterReader
		}
		if val, ok := m2.GetValue(ctx, meterName, startTime, endTime, dims); ok {
			return val, ok
		}
	}
	return 0, false
}

func (m *multiMeterUser) Check(ctx context.Context, meterName string, value float64, dims meters.Dimensions) (bool, error) {
	for _, m := range m.mets {
		m2, ok := m.(meters.MeterReader)
		if !ok {
			continue // Skip if the meter does not implement MeterReader
		}
		if ok, err := m2.Check(ctx, meterName, value, dims); !ok || err != nil {
			return ok, err
		}
	}
	return true, nil
}
