package meters

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/interline-io/log"
	"github.com/interline-io/transitland-mw/auth/authn"
)

var meterCtxKey = struct{ name string }{"apiMeter"}

type ApiMeter interface {
	Meter(string, float64, Dimensions, string) error
	AddDimension(string, string, string)
	GetValue(string, time.Time, time.Time, Dimensions) (float64, bool)
	Check(string, float64, Dimensions) (bool, error)
}

type MeterProvider interface {
	// GetValue(MeterUser, string, time.Time, time.Time, Dimensions) (float64, bool)
	NewMeter(MeterUser) ApiMeter
	Close() error
	Flush() error
}

type MeterUser interface {
	ID() string
	GetExternalData(string) (string, bool)
}

type responseWriterWrapper struct {
	statusCode int
	http.ResponseWriter
}

func (w *responseWriterWrapper) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func WithMeter(apiMeter MeterProvider, meterName string, meterValue float64, dims Dimensions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap ResponseWriter so we can check status code
			wr := &responseWriterWrapper{ResponseWriter: w}

			// Make ctxMeter available in context
			ctx := r.Context()
			ctxUser := authn.ForContext(ctx)
			meterLog := log.With().
				Str("user", ctxUser.ID()).
				Str("meter", meterName).
				Float64("meter_value", meterValue).
				Logger()

			ctxMeter := apiMeter.NewMeter(ctxUser)
			r = r.WithContext(context.WithValue(ctx, meterCtxKey, ctxMeter))

			// Check if we are within available rate limits
			meterCheck, meterErr := ctxMeter.Check(meterName, meterValue, dims)
			if meterErr != nil {
				meterLog.Error().Err(meterErr).Msg("meter check error")
			}
			if !meterCheck {
				meterLog.Trace().Msg("not metering event due to rate limit 429")
				http.Error(w, "429", http.StatusTooManyRequests)
				return
			}

			// Call next handler
			next.ServeHTTP(wr, r)

			// Meter the event if status code is less than 400
			if wr.statusCode >= 400 {
				meterLog.Trace().Int("code", wr.statusCode).Msg("not metering event due to status code")
				return
			}

			// Get request ID from context
			reqID := log.GetReqID(r.Context())

			if err := ctxMeter.Meter(meterName, meterValue, dims, reqID); err != nil {
				meterLog.Error().Err(err).Msg("failed to meter event")
			}
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
