package local

import (
	"sync"
	"time"

	"github.com/interline-io/log"
	"github.com/interline-io/transitland-mw/meters"
)

type MeterUser = meters.MeterUser
type ApiMeter = meters.ApiMeter
type Dimension = meters.Dimension
type Dimensions = meters.Dimensions

type LocalMeterProvider struct {
	values map[string]localMeterUserEvents
	lock   sync.Mutex
}

func NewLocalMeterProvider() *LocalMeterProvider {
	return &LocalMeterProvider{
		values: map[string]localMeterUserEvents{},
	}
}

func (m *LocalMeterProvider) Flush() error {
	return nil
}

func (m *LocalMeterProvider) Close() error {
	return nil
}

func (m *LocalMeterProvider) NewMeter(user MeterUser) ApiMeter {
	return &localUserMeter{
		user: user,
		mp:   m,
	}
}

func (m *LocalMeterProvider) sendMeter(u MeterUser, meterName string, value float64, dims []Dimension) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	a, ok := m.values[meterName]
	if !ok {
		a = localMeterUserEvents{}
		m.values[meterName] = a
	}
	userName := ""
	if u != nil {
		userName = u.ID()
	}
	event := localMeterEvent{
		value: value,
		time:  time.Now().In(time.UTC),
		dims:  dims,
	}
	a[userName] = append(a[userName], event)
	log.Trace().
		Str("user", userName).
		Str("meter", meterName).
		Float64("meter_value", value).
		Msg("meter")
	return nil
}

func (m *LocalMeterProvider) GetValue(u MeterUser, meterName string, startTime time.Time, endTime time.Time, checkDims Dimensions) (float64, bool) {
	m.lock.Lock()
	defer m.lock.Unlock()
	a, ok := m.values[meterName]
	if !ok {
		return 0, false
	}
	total := 0.0
	for _, userEvent := range a[u.ID()] {
		match := true
		if userEvent.time.Equal(endTime) || userEvent.time.After(endTime) {
			// fmt.Println("not matched on end time", userEvent.time, endTime)
			match = false
		}
		if userEvent.time.Before(startTime) {
			// fmt.Println("not matched on start time", userEvent.time, startTime)
			match = false
		}
		if !meters.DimsContainedIn(checkDims, userEvent.dims) {
			// fmt.Println("not matched on dims")
			match = false
		}
		if match {
			// fmt.Println("matched:", userEvent.value)
			total += userEvent.value
		}
	}
	return total, ok
}

type eventAddDim struct {
	MeterName string
	Key       string
	Value     string
}

type localUserMeter struct {
	user    MeterUser
	addDims []eventAddDim
	mp      *LocalMeterProvider
}

func (m *localUserMeter) Meter(meterName string, value float64, extraDimensions Dimensions) error {
	// Copy in matching dimensions set through AddDimension
	var eventDims []Dimension
	for _, addDim := range m.addDims {
		if addDim.MeterName == meterName {
			eventDims = append(eventDims, Dimension{Key: addDim.Key, Value: addDim.Value})
		}
	}
	eventDims = append(eventDims, extraDimensions...)
	return m.mp.sendMeter(m.user, meterName, value, eventDims)
}

func (m *localUserMeter) AddDimension(meterName string, key string, value string) {
	m.addDims = append(m.addDims, eventAddDim{MeterName: meterName, Key: key, Value: value})
}

func (m *localUserMeter) GetValue(meterName string, startTime time.Time, endTime time.Time, dims Dimensions) (float64, bool) {
	return m.mp.GetValue(m.user, meterName, startTime, endTime, dims)
}

///////////

type localMeterEvent struct {
	time  time.Time
	dims  []Dimension
	value float64
}

type localMeterUserEvents map[string][]localMeterEvent
