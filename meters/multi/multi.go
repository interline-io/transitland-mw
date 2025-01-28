package multi

import (
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

func (m *MultiMeterProvider) NewMeter(user meters.MeterUser) meters.ApiMeter {
	var mets []meters.ApiMeter
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

func (m *MultiMeterProvider) GetValue(u meters.MeterUser, meterName string, startTime time.Time, endTime time.Time, checkDims meters.Dimensions) (float64, bool) {
	for _, m := range m.meters {
		if val, ok := m.GetValue(u, meterName, startTime, endTime, checkDims); ok {
			return val, ok
		}
	}
	return 0, false
}

type multiMeterUser struct {
	mets []meters.ApiMeter
}

func (m *multiMeterUser) Meter(meterName string, value float64, extraDimensions meters.Dimensions) error {
	for _, m := range m.mets {
		if err := m.Meter(meterName, value, extraDimensions); err != nil {
			return err
		}
	}
	return nil
}

func (m *multiMeterUser) AddDimension(meterName string, key string, value string) {
	for _, m := range m.mets {
		m.AddDimension(meterName, key, value)
	}
}

func (m *multiMeterUser) GetValue(meterName string, startTime time.Time, endTime time.Time, dims meters.Dimensions) (float64, bool) {
	for _, m := range m.mets {
		if val, ok := m.GetValue(meterName, startTime, endTime, dims); ok {
			return val, ok
		}
	}
	return 0, false

}
