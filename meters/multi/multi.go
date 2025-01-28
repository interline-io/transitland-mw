package multi

import (
	"time"

	"github.com/interline-io/transitland-mw/meters"
)

func init() {
	var _ meters.MeterProvider = &MultiMeter{}
}

type MultiMeter struct {
	meters []meters.MeterProvider
}

func (m *MultiMeter) NewMeter(user meters.MeterUser) meters.ApiMeter {
	var mets []meters.ApiMeter
	for _, m := range m.meters {
		mets = append(mets, m.NewMeter(user))
	}
	return &multiUserMeter{
		mets: mets,
	}
}

func (m *MultiMeter) Flush() error {
	for _, m := range m.meters {
		if err := m.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func (m *MultiMeter) Close() error {
	for _, m := range m.meters {
		if err := m.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (m *MultiMeter) AddDimension(meterName string, key string, value string) {
	// for _, m := range m.meters {
	// 	m.AddDimension(meterName, key, value)
	// }
}

func (m *MultiMeter) GetValue(u meters.MeterUser, meterName string, startTime time.Time, endTime time.Time, checkDims meters.Dimensions) (float64, bool) {
	for _, m := range m.meters {
		if val, ok := m.GetValue(u, meterName, startTime, endTime, checkDims); ok {
			return val, ok
		}
	}
	return 0, false
}

type multiUserMeter struct {
	mets []meters.ApiMeter
}

func (m *multiUserMeter) Meter(meterName string, value float64, extraDimensions meters.Dimensions) error {
	for _, m := range m.mets {
		if err := m.Meter(meterName, value, extraDimensions); err != nil {
			return err
		}
	}
	return nil
}

func (m *multiUserMeter) AddDimension(meterName string, key string, value string) {
	for _, m := range m.mets {
		m.AddDimension(meterName, key, value)
	}
}

func (m *multiUserMeter) GetValue(meterName string, startTime time.Time, endTime time.Time, dims meters.Dimensions) (float64, bool) {
	for _, m := range m.mets {
		if val, ok := m.GetValue(meterName, startTime, endTime, dims); ok {
			return val, ok
		}
	}
	return 0, false

}
