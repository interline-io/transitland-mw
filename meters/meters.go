package meters

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/interline-io/log"
	"github.com/interline-io/transitland-mw/auth/authn"
)

// MeterEvent represents a metered event with a name, value, timestamp, dimensions, and request ID.
type MeterEvent struct {
	Name       string
	Value      float64
	Timestamp  time.Time
	Dimensions Dimensions
	RequestID  string // Request ID associated with the event, useful for tracing
	StatusCode int    // HTTP status code of the request that generated this event
	Success    bool   // Indicates if the event was successful (e.g., HTTP status < 400)
}

// NewMeterEvent creates a new MeterEvent with the current time in UTC.
func NewMeterEvent(name string, value float64, dims Dimensions) MeterEvent {
	return MeterEvent{
		Name:       name,
		Value:      value,
		Timestamp:  time.Now().In(time.UTC),
		Dimensions: dims,
	}
}

// MeterRecorder is an interface for recording metered events.
// WithDimenions returns a new MeterRecorder with the specified dimension.
// which will be applied to all subsequent events recorded by this MeterRecorder.
type MeterRecorder interface {
	Meter(context.Context, MeterEvent) error
	WithDimension(key, value string) MeterRecorder
}

// MeterReader is an interface for reading metered values and checking rate limits.
type MeterReader interface {
	GetValue(context.Context, string, time.Time, time.Time, Dimensions) (float64, bool)
	Check(context.Context, string, float64, Dimensions) (bool, error)
}

// Meterer combines both MeterReader and MeterRecorder interfaces.
type Meterer interface {
	MeterReader
	MeterRecorder
}

// MeterProvider is an interface for creating new Meterers.
// It also provides methods for closing the provider and flushing any buffered data.
// The NewMeter method takes a MeterUser, which provides user-specific context for metering.
// The Close method is used to clean up resources, and Flush is used to ensure all data is written out.
type MeterProvider interface {
	// NewMeter creates a new Meterer for the given MeterUser.
	NewMeter(MeterUser) Meterer
	// Close and Flush are used to clean up resources and ensure all data is written out.
	Close() error
	// Flush is used to ensure all buffered data is written out.
	Flush() error
}

// MeterUser is an interface representing a user for metering purposes.
// It provides an ID method to get the user's identifier and a GetExternalData method
// to retrieve external data associated with the user, which can be used for metering purposes.
type MeterUser interface {
	ID() string
	GetExternalData(string) (string, bool)
}

// WithMeter is a middleware function that wraps an http.Handler to provide metering functionality.
// It checks the rate limits for a given meter and records events if the request is successful.
// It uses the provided MeterProvider to create a Meterer for the current user context.
// The meterName is the name of the meter, meterValue is the value to be recorded,
// and dims are the dimensions associated with the meter event.
// If the rate limit is exceeded, it responds with a 429 Too Many Requests status code.
// If the request is successful (status code < 400), it meters the event using the Meterer.
func WithMeter(apiMeter MeterProvider, meterName string, meterValue float64, dims Dimensions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap ResponseWriter so we can check status code
			wr := &responseWriterWrapper{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			// Make ctxMeter available in context
			ctx := r.Context()
			ctxUser := authn.ForContext(ctx)
			meterLog := log.With().
				Str("user", ctxUser.ID()).
				Str("meter", meterName).
				Float64("meter_value", meterValue).
				Logger()

			ctxMeter := apiMeter.NewMeter(ctxUser)
			r = r.WithContext(InjectContext(ctx, ctxMeter))

			// Check if we are within available rate limits
			meterCheck, meterErr := ctxMeter.Check(ctx, meterName, meterValue, dims)
			if meterErr != nil {
				meterLog.Error().Err(meterErr).Msg("meter check error")
			}
			if !meterCheck {
				meterLog.Debug().Msg("not metering event due to rate limit 429")
				http.Error(w, "429", http.StatusTooManyRequests)
				return
			}

			// Call next handler
			next.ServeHTTP(wr, r)

			// Create a new MeterEvent with the current time in UTC
			event := NewMeterEvent(meterName, meterValue, dims)
			event.RequestID = log.GetReqID(r.Context())
			event.StatusCode = wr.statusCode
			event.Success = wr.statusCode < 400

			// Fetch meterer again from context, as it may have been modified by the next handler
			if err := ForContext(ctx).Meter(ctx, event); err != nil {
				meterLog.Error().Err(err).Msg("failed to meter event")
			}
		})
	}
}

type responseWriterWrapper struct {
	statusCode int
	http.ResponseWriter
}

func (w *responseWriterWrapper) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

var meterCtxKey = struct{ name string }{"apiMeter"}

func InjectContext(ctx context.Context, m Meterer) context.Context {
	return context.WithValue(ctx, meterCtxKey, m)
}

func ForContext(ctx context.Context) Meterer {
	raw, _ := ctx.Value(meterCtxKey).(Meterer)
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
