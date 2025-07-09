package otel

import (
	"net/http"

	"github.com/riandyrn/otelchi"
	"github.com/riverqueue/rivercontrib/otelriver"
)

// Config holds OpenTelemetry configuration for HTTP, River, and Database
type Config struct {
	ServiceName  string
	DurationUnit string // "ms" or "s" - used for River
	// Tracing configuration flags
	EnableHTTPTracing  bool
	EnableRiverTracing bool
	EnableDBTracing    bool
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		DurationUnit:       "s",
		EnableHTTPTracing:  true,
		EnableRiverTracing: true,
		EnableDBTracing:    true,
	}
}

// HTTP Middleware Functions

// NewHTTPMiddleware creates a new HTTP OpenTelemetry middleware
func NewHTTPMiddleware(serviceName string) func(http.Handler) http.Handler {
	return otelchi.Middleware(serviceName)
}

// NewHTTPMiddlewareWithConfig creates a new HTTP OpenTelemetry middleware with custom configuration
func NewHTTPMiddlewareWithConfig(config *Config) func(http.Handler) http.Handler {
	if !config.EnableHTTPTracing {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return otelchi.Middleware(config.ServiceName)
}

// River Middleware Functions

// NewRiverMiddleware creates a River OpenTelemetry middleware
func NewRiverMiddleware(cfg *Config) *otelriver.Middleware {
	if !cfg.EnableRiverTracing {
		return nil
	}

	middlewareConfig := &otelriver.MiddlewareConfig{
		DurationUnit: cfg.DurationUnit,
	}

	return otelriver.NewMiddleware(middlewareConfig)
}
