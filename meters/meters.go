package meters

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/interline-io/transitland-mw/auth/authn"
)

var meterCtxKey = struct{ name string }{"apiMeter"}

type ApiMeter interface {
	Meter(string, float64, Dimensions) error
	AddDimension(string, string, string)
	GetValue(string, time.Time, time.Time, Dimensions) (float64, bool)
}

type MeterProvider interface {
	GetValue(MeterUser, string, time.Time, time.Time, Dimensions) (float64, bool)
	NewMeter(MeterUser) ApiMeter
	Close() error
	Flush() error
}

type MeterUser interface {
	ID() string
	GetExternalData(string) (string, bool)
}

func WithMeter(apiMeter MeterProvider, meterName string, meterValue float64, dims Dimensions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Make ctxMeter available in context
			ctx := r.Context()
			ctxMeter := apiMeter.NewMeter(authn.ForContext(ctx))
			r = r.WithContext(context.WithValue(ctx, meterCtxKey, ctxMeter))
			if err := ctxMeter.Meter(meterName, meterValue, dims); err != nil {
				http.Error(w, "429", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func ForContext(ctx context.Context) ApiMeter {
	raw, _ := ctx.Value(meterCtxKey).(ApiMeter)
	return raw
}

type Dimension struct {
	Key   string
	Value string
}

type Dimensions []Dimension

func DimsContainedIn(checkDims Dimensions, eventDims Dimensions) bool {
	for _, matchDim := range checkDims {
		match := false
		for _, ed := range eventDims {
			if ed.Key == matchDim.Key && ed.Value == matchDim.Value {
				match = true
			}
		}
		if !match {
			return false
		}
	}
	return true
}

// Periods

type UserMeterLimit struct {
	User      string
	MeterName string
	Dims      Dimensions
	Period    string
	Limit     float64
}

func (lim *UserMeterLimit) Span() (time.Time, time.Time) {
	a, b, err := PeriodSpan(lim.Period)
	if err != nil {
		panic(err)
	}
	return a, b
}

func PeriodSpan(period string) (time.Time, time.Time, error) {
	now := time.Now().In(time.UTC)
	d1 := now
	d2 := now
	if period == "hourly" {
		d1 = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, time.UTC)
		d2 = d1.Add(3600 * time.Second)
	} else if period == "daily" {
		d1 = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		d2 = d1.AddDate(0, 0, 1)
	} else if period == "monthly" {
		d1 = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		d2 = d1.AddDate(0, 1, 0)
	} else if period == "yearly" {
		d1 = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		d2 = d1.AddDate(1, 0, 0)
	} else if period == "total" {
		d1 = time.Unix(0, 0)
		d2 = time.Unix(1<<63-1, 0)
	} else {
		return now, now, fmt.Errorf("unknown period: %s", period)
	}
	return d1, d2, nil
}
