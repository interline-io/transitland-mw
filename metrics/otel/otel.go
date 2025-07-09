package otel

import (
	"net/http"

	"github.com/riandyrn/otelchi"
	"github.com/riverqueue/rivercontrib/otelriver"
	"go.opentelemetry.io/otel/trace"
)

// Config holds OpenTelemetry configuration for both HTTP and River
type Config struct {
	ServiceName  string
	DurationUnit string // "ms" or "s" - used for River
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		DurationUnit: "s",
	}
}

// HTTP Middleware Functions

// NewHTTPMiddleware creates a new HTTP OpenTelemetry middleware using otelchi
func NewHTTPMiddleware(serviceName string) func(http.Handler) http.Handler {
	return otelchi.Middleware(serviceName)
}

// NewHTTPMiddlewareWithConfig creates a new HTTP OpenTelemetry middleware with custom configuration
func NewHTTPMiddlewareWithConfig(config *Config) func(http.Handler) http.Handler {
	return otelchi.Middleware(config.ServiceName)
}

// River Middleware Functions

// NewRiverMiddleware creates a River OpenTelemetry middleware with the given configuration
func NewRiverMiddleware(cfg *Config) *otelriver.Middleware {
	middlewareConfig := &otelriver.MiddlewareConfig{
		DurationUnit: cfg.DurationUnit,
	}

	return otelriver.NewMiddleware(middlewareConfig)
}

// NewRiverMiddlewareWithProviders creates a River OpenTelemetry middleware with custom providers
func NewRiverMiddlewareWithProviders(cfg *Config, tracerProvider trace.TracerProvider) *otelriver.Middleware {
	middlewareConfig := &otelriver.MiddlewareConfig{
		DurationUnit:   cfg.DurationUnit,
		TracerProvider: tracerProvider,
	}

	return otelriver.NewMiddleware(middlewareConfig)
}
