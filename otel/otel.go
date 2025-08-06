// Package otel provides OpenTelemetry tracing middleware and configuration utilities
// for HTTP servers and background job processors.
//
// This package offers a comprehensive OpenTelemetry setup with support for multiple
// exporters (console, OTLP), automatic span enrichment, and easy integration with
// chi routers and River job queues. It handles the complexity of OpenTelemetry
// configuration while providing sensible defaults and extensive customization options.
//
// Key features:
//   - Multiple exporter support (console for development, OTLP for production)
//   - Automatic HTTP request tracing with user context and request metadata
//   - River job queue tracing integration
//   - Flexible configuration via environment variables or Config struct
//   - Support for REST and GraphQL API types with appropriate span attributes
//   - Production-ready features like retry logic, compression, and authentication headers
//
// Basic usage:
//
//	// Initialize with environment variables
//	err := otel.InitSDK("my-service")
//
//	// Or with custom configuration
//	cfg := &otel.Config{
//	    ServiceName:     "my-service",
//	    TracesExporter:  "console",
//	    Environment:     "development",
//	    ApiType:         "rest",
//	}
//	err := otel.InitSDKWithConfig("my-service", cfg)
//
//	// Add HTTP middleware
//	r := chi.NewRouter()
//	r.Use(otel.GetEnrichedOTelMiddleware(cfg))
package otel

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/interline-io/transitland-mw/auth/authn"
	"github.com/riandyrn/otelchi"
	"github.com/riverqueue/rivercontrib/otelriver"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
)

/*
Supported Environment Variables:

Core Configuration:
- OTEL_ENVIRONMENT: Deployment environment (default: "development")
- OTEL_SERVICE_VERSION: Service version (default: "1.0.0")
- OTEL_TRACES_EXPORTER: Exporter type ("console", "otlp", or "none" to disable)

Console Exporter (stdouttrace):
- OTEL_STDOUT_WITHOUT_TIMESTAMPS: "true" to exclude timestamps from console output
- OTEL_STDOUT_WRITER: Custom writer destination (e.g., "stderr", "file:/path/to/file")
- OTEL_STDOUT_PRETTY_PRINT: "false" to disable pretty printing (default: "true")

OTLP Exporter:
- OTEL_EXPORTER_OTLP_ENDPOINT: OTLP endpoint URL (default: "http://grafana-alloy:4317")
- OTEL_EXPORTER_OTLP_TIMEOUT: Request timeout (supports "10s", "30s" or "10000" for milliseconds)
- OTEL_EXPORTER_OTLP_HEADERS: Custom headers in format "key1=value1,key2=value2"
- OTEL_EXPORTER_OTLP_COMPRESSION: "gzip" to enable compression
- OTEL_EXPORTER_OTLP_URL_PATH: Custom URL path (default: "/v1/traces")
- OTEL_EXPORTER_OTLP_RETRY_ENABLED: "true" to enable retry with exponential backoff

Example usage:
  # Simple usage with environment variables
  OTEL_TRACES_EXPORTER=console OTEL_ENVIRONMENT=development
  cfg := otel.GetConfigFromEnv()
  err := otel.InitSDKWithConfig("my-service", cfg)

  # Or use the convenience function that reads from environment automatically
  err := otel.InitSDK("my-service")

  # Disable OTEL completely
  OTEL_TRACES_EXPORTER=none

  # Production setup
  OTEL_ENVIRONMENT=production
  OTEL_SERVICE_VERSION=2.1.0
  OTEL_EXPORTER_OTLP_ENDPOINT=http://grafana-alloy:4317
  OTEL_EXPORTER_OTLP_TIMEOUT=30s
  OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer token123
  OTEL_EXPORTER_OTLP_COMPRESSION=gzip
  OTEL_EXPORTER_OTLP_RETRY_ENABLED=true

  # Console exporter options
  OTEL_STDOUT_WITHOUT_TIMESTAMPS=true
  OTEL_STDOUT_WRITER=stderr
  OTEL_STDOUT_PRETTY_PRINT=false

  # Manual configuration (alternative to environment variables)
  cfg := &otel.Config{
    ServiceName:        "my-service",
    TracesExporter:     "console",
    Environment:        "development",
    EnableHTTPTracing:  true,
    EnableRiverTracing: true,
  }
  err := otel.InitSDKWithConfig("my-service", cfg)
*/

// Config holds OpenTelemetry configuration for HTTP and River middleware.
// This struct centralizes all OpenTelemetry settings and can be populated
// from environment variables using GetConfigFromEnv() or configured manually.
type Config struct {
	ServiceName    string // Service name for telemetry resource attribution
	DurationUnit   string // "ms" or "s" - duration unit used for River job tracing
	ApiType        string // "rest", "graphql", or "" - determines span attribute enrichment strategy
	TracesExporter string // "console", "otlp", or "none" - exporter type
	Environment    string // deployment environment (e.g., "development", "production")
	ServiceVersion string // service version for telemetry resource attribution
	OTLPEndpoint   string // OTLP endpoint URL for production tracing
	Enabled        bool   // whether tracing is enabled (derived from TracesExporter != "none")

	// Tracing configuration flags
	EnableHTTPTracing  bool // whether to enable HTTP request tracing
	EnableRiverTracing bool // whether to enable River job tracing

	// Console exporter options (for development and debugging)
	StdoutWithoutTimestamps bool   // exclude timestamps from console output
	StdoutWriter            string // "stderr", "stdout", or "file:/path/to/file"
	StdoutPrettyPrint       bool   // enable pretty printing of console output

	// OTLP exporter options (for production tracing)
	OTLPTimeout      string            // timeout as duration string (e.g. "30s") or milliseconds
	OTLPHeaders      map[string]string // custom headers for authentication/authorization
	OTLPCompression  string            // "gzip" to enable compression, "" to disable
	OTLPURLPath      string            // custom URL path for OTLP endpoint
	OTLPRetryEnabled bool              // whether to enable retry with exponential backoff
}

// DefaultConfig returns a default configuration with sensible defaults.
// Tracing is disabled by default (TracesExporter: "none").
// Enable tracing by setting OTEL_TRACES_EXPORTER environment variable or
// configuring TracesExporter in the returned Config.
func DefaultConfig() *Config {
	return &Config{
		DurationUnit:            "s",
		EnableHTTPTracing:       true,
		EnableRiverTracing:      true,
		Environment:             "development",
		ServiceVersion:          "1.0.0",
		TracesExporter:          "none",
		OTLPEndpoint:            "http://grafana-alloy:4317",
		StdoutPrettyPrint:       true,
		StdoutWithoutTimestamps: false,
		OTLPHeaders:             make(map[string]string),
	}
}

// GetConfigFromEnv creates a Config from environment variables.
// This function reads all supported OTEL_* environment variables and
// populates a Config struct with their values, falling back to defaults
// from DefaultConfig() for any unset variables.
func GetConfigFromEnv() *Config {
	cfg := DefaultConfig()

	// Core configuration
	if env := os.Getenv("OTEL_ENVIRONMENT"); env != "" {
		cfg.Environment = env
	}
	if serviceVersion := os.Getenv("OTEL_SERVICE_VERSION"); serviceVersion != "" {
		cfg.ServiceVersion = serviceVersion
	}
	if tracesExporter := os.Getenv("OTEL_TRACES_EXPORTER"); tracesExporter != "" {
		cfg.TracesExporter = tracesExporter
	}
	cfg.Enabled = cfg.TracesExporter != "none"

	// Console exporter options
	cfg.StdoutWithoutTimestamps = os.Getenv("OTEL_STDOUT_WITHOUT_TIMESTAMPS") == "true"
	if writer := os.Getenv("OTEL_STDOUT_WRITER"); writer != "" {
		cfg.StdoutWriter = writer
	}
	if prettyPrint := os.Getenv("OTEL_STDOUT_PRETTY_PRINT"); prettyPrint == "false" {
		cfg.StdoutPrettyPrint = false
	}

	// OTLP exporter options
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		cfg.OTLPEndpoint = endpoint
	}
	if timeout := os.Getenv("OTEL_EXPORTER_OTLP_TIMEOUT"); timeout != "" {
		cfg.OTLPTimeout = timeout
	}
	if headers := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); headers != "" {
		// Parse headers in format "key1=value1,key2=value2"
		headerMap := make(map[string]string)
		for _, pair := range strings.Split(headers, ",") {
			if kv := strings.SplitN(strings.TrimSpace(pair), "=", 2); len(kv) == 2 {
				headerMap[kv[0]] = kv[1]
			}
		}
		cfg.OTLPHeaders = headerMap
	}
	if compression := os.Getenv("OTEL_EXPORTER_OTLP_COMPRESSION"); compression != "" {
		cfg.OTLPCompression = compression
	}
	if urlPath := os.Getenv("OTEL_EXPORTER_OTLP_URL_PATH"); urlPath != "" {
		cfg.OTLPURLPath = urlPath
	}
	cfg.OTLPRetryEnabled = os.Getenv("OTEL_EXPORTER_OTLP_RETRY_ENABLED") == "true"

	return cfg
}

// buildConsoleExporterOptions builds the options for the console exporter
// based on the provided configuration. Handles pretty printing, timestamps,
// and custom writer destinations (stderr, stdout, or file paths).
func buildConsoleExporterOptions(cfg *Config) []stdouttrace.Option {
	var opts []stdouttrace.Option

	// Start with pretty print if enabled
	if cfg.StdoutPrettyPrint {
		opts = append(opts, stdouttrace.WithPrettyPrint())
	}

	// Add timestamps option
	if cfg.StdoutWithoutTimestamps {
		opts = append(opts, stdouttrace.WithoutTimestamps())
	}

	// Allow custom writer destination
	if cfg.StdoutWriter != "" {
		var writer io.Writer
		switch cfg.StdoutWriter {
		case "stderr":
			writer = os.Stderr
		case "stdout":
			writer = os.Stdout
		default:
			// Try to open as file path
			if strings.HasPrefix(cfg.StdoutWriter, "file:") {
				filePath := strings.TrimPrefix(cfg.StdoutWriter, "file:")
				if file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
					writer = file
				}
			}
		}
		if writer != nil {
			opts = append(opts, stdouttrace.WithWriter(writer))
		}
	}

	return opts
}

// Initialization Functions

// InitSDK initializes the OpenTelemetry SDK with configuration from environment variables.
// This is a convenience function that calls GetConfigFromEnv() and InitSDKWithConfig().
// Returns nil if tracing is disabled (OTEL_TRACES_EXPORTER=none).
func InitSDK(serviceName string) error {
	cfg := GetConfigFromEnv()
	return InitSDKWithConfig(serviceName, cfg)
}

// InitSDKWithConfig initializes the OpenTelemetry SDK with the provided configuration.
// Supports console exporter (for development) and OTLP exporter (for production).
// Sets up the global tracer provider with appropriate resource attributes.
// Returns nil if tracing is disabled (TracesExporter: "none").
func InitSDKWithConfig(serviceName string, cfg *Config) error {
	// Create resource with service information
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			semconv.DeploymentEnvironment(cfg.Environment),
		),
	)
	if err != nil {
		return err
	}

	var exporter sdktrace.SpanExporter
	var err2 error

	switch cfg.TracesExporter {
	case "none":
		// No exporter - tracing is disabled
		return nil
	case "console":
		// Console exporter for development and debugging
		opts := buildConsoleExporterOptions(cfg)
		exporter, err2 = stdouttrace.New(opts...)
	case "otlp":
		// OTLP exporter for production (sends to Grafana Alloy or other OTLP-compatible backends)
		// Build client options based on configuration
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(cfg.OTLPEndpoint),
			otlptracehttp.WithInsecure(), // For development, can be overridden
		}

		// Add timeout if specified (supports both duration strings and milliseconds)
		if cfg.OTLPTimeout != "" {
			// Try parsing as duration first (e.g., "10s", "30s")
			if duration, err := time.ParseDuration(cfg.OTLPTimeout); err == nil {
				opts = append(opts, otlptracehttp.WithTimeout(duration))
			} else {
				// Try parsing as milliseconds (e.g., "10000")
				if ms, err := strconv.Atoi(cfg.OTLPTimeout); err == nil {
					opts = append(opts, otlptracehttp.WithTimeout(time.Duration(ms)*time.Millisecond))
				}
			}
		}

		// Add headers if specified (useful for authentication)
		if len(cfg.OTLPHeaders) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(cfg.OTLPHeaders))
		}

		// Add compression if specified
		if cfg.OTLPCompression == "gzip" {
			opts = append(opts, otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
		}

		// Add custom URL path if specified
		if cfg.OTLPURLPath != "" {
			opts = append(opts, otlptracehttp.WithURLPath(cfg.OTLPURLPath))
		}

		// Add retry configuration if specified
		if cfg.OTLPRetryEnabled {
			// Default retry config with exponential backoff
			retryConfig := otlptracehttp.RetryConfig{
				Enabled:         true,
				InitialInterval: 5 * time.Second,
				MaxInterval:     30 * time.Second,
				MaxElapsedTime:  60 * time.Second,
			}
			opts = append(opts, otlptracehttp.WithRetry(retryConfig))
		}

		client := otlptracehttp.NewClient(opts...)
		exporter, err2 = otlptrace.New(context.Background(), client)
	default:
		return fmt.Errorf("unsupported OpenTelemetry exporter type: %s", cfg.TracesExporter)
	}

	if err2 != nil {
		return err2
	}

	// Create trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	// Set global trace provider
	otel.SetTracerProvider(tp)

	return nil
}

// Middleware Functions

// HTTP Middleware Functions

// NewMiddleware creates a basic HTTP OpenTelemetry middleware using otelchi.
// For more advanced span enrichment, use GetEnrichedOTelMiddleware instead.
func NewMiddleware(serviceName string) func(http.Handler) http.Handler {
	return otelchi.Middleware(serviceName)
}

// NewMiddlewareWithConfig creates an HTTP OpenTelemetry middleware with custom configuration.
// Returns a no-op middleware if EnableHTTPTracing is false in the config.
func NewMiddlewareWithConfig(config *Config) func(http.Handler) http.Handler {
	if !config.EnableHTTPTracing {
		return func(next http.Handler) http.Handler {
			return next
		}
	}
	return otelchi.Middleware(config.ServiceName)
}

// River Middleware Functions

// NewRiverMiddleware creates a River job queue OpenTelemetry middleware.
// Returns nil if EnableRiverTracing is false in the config.
// The middleware respects the DurationUnit setting from the config.
func NewRiverMiddleware(cfg *Config) *otelriver.Middleware {
	if !cfg.EnableRiverTracing {
		return nil
	}

	middlewareConfig := &otelriver.MiddlewareConfig{
		DurationUnit: cfg.DurationUnit,
	}

	return otelriver.NewMiddleware(middlewareConfig)
}

// GetEnrichedOTelMiddleware returns an OpenTelemetry HTTP middleware with enriched spans.
// This middleware automatically adds comprehensive span attributes including:
//   - User information (ID, name, email, roles) from authentication context
//   - Request metadata (user agent, content length, client IP)
//   - API-specific attributes based on cfg.ApiType (REST path/query params, GraphQL operation type)
//   - Request ID and API key presence indicators
//
// Returns a no-op middleware if tracing is disabled (cfg.Enabled == false).
func GetEnrichedOTelMiddleware(cfg *Config) func(http.Handler) http.Handler {
	// Return no-op middleware if tracing is disabled
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}

	baseMiddleware := NewMiddleware(cfg.ServiceName)

	return func(next http.Handler) http.Handler {
		return baseMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			span := trace.SpanFromContext(r.Context())

			// Add user information if available
			if user := authn.ForContext(r.Context()); user != nil {
				span.SetAttributes(
					attribute.String("user.id", user.ID()),
					attribute.String("user.name", user.Name()),
					attribute.String("user.email", user.Email()),
				)
				if roles := user.Roles(); len(roles) > 0 {
					span.SetAttributes(attribute.StringSlice("user.roles", roles))
				}
			}

			// Add request metadata
			span.SetAttributes(attribute.String("http.user_agent", r.UserAgent()))

			// Handle parameters based on API type from config
			switch cfg.ApiType {
			case "rest":
				// Add URL parameters from chi router context
				span.SetAttributes(attribute.String("api.type", cfg.ApiType))
				if rctx := chi.RouteContext(r.Context()); rctx != nil {
					// Add path parameters only if they exist
					for i, k := range rctx.URLParams.Keys {
						if i < len(rctx.URLParams.Values) && rctx.URLParams.Values[i] != "" {
							span.SetAttributes(attribute.String("http.path_param."+k, rctx.URLParams.Values[i]))
						}
					}
				}
				// Add query parameters
				query := r.URL.Query()
				for k, v := range query {
					if len(v) > 0 {
						span.SetAttributes(attribute.String("http.query_param."+k, v[0]))
					}
				}
			case "graphql":
				// For GraphQL, check if it's a POST with JSON body
				span.SetAttributes(attribute.String("api.type", cfg.ApiType))
				if r.Method == "POST" && r.Header.Get("Content-Type") == "application/json" {
					// We'll only track that it's a GraphQL operation
					span.SetAttributes(attribute.String("graphql.request_type", "operation"))
				} else if r.Method == "GET" {
					// GET requests to GraphQL endpoint are usually schema introspection
					span.SetAttributes(attribute.String("graphql.request_type", "introspection"))
				}
			}

			// Add content length for all JSON requests
			if r.Header.Get("Content-Type") == "application/json" {
				if r.ContentLength > 0 && r.ContentLength < 1024*1024 { // 1MB limit
					span.SetAttributes(attribute.String("http.content_length", fmt.Sprintf("%d", r.ContentLength)))
				}
			}

			// Add client IP (prioritize Kong headers)
			if xRealIP := r.Header.Get("X-Real-IP"); xRealIP != "" {
				span.SetAttributes(attribute.String("http.real_ip", xRealIP))
			} else if xForwardedFor := r.Header.Get("X-Forwarded-For"); xForwardedFor != "" {
				span.SetAttributes(attribute.String("http.forwarded_for", xForwardedFor))
			} else {
				span.SetAttributes(attribute.String("http.remote_addr", r.RemoteAddr))
			}

			// Add request ID and API key
			if requestID := middleware.GetReqID(r.Context()); requestID != "" {
				span.SetAttributes(attribute.String("request.id", requestID))
			}
			// Just mark if an API key is present, never include the actual key
			if apiKey := r.Header.Get("apikey"); apiKey != "" {
				span.SetAttributes(attribute.String("http.apikey", "present"))
			}

			next.ServeHTTP(w, r)
		}))
	}
}
