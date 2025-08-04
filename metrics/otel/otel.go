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

	"github.com/riandyrn/otelchi"
	"github.com/riverqueue/rivercontrib/otelriver"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
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
*/

// Config holds OpenTelemetry configuration for HTTP and River
type Config struct {
	ServiceName  string
	DurationUnit string // "ms" or "s" - used for River
	// Tracing configuration flags
	EnableHTTPTracing  bool
	EnableRiverTracing bool
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		DurationUnit:       "s",
		EnableHTTPTracing:  true,
		EnableRiverTracing: true,
	}
}

// InitSDK initializes the OpenTelemetry SDK with appropriate exporter
func InitSDK(serviceName string) error {
	// Get environment variables for resource configuration
	env := os.Getenv("OTEL_ENVIRONMENT")
	if env == "" {
		env = "development"
	}

	serviceVersion := os.Getenv("OTEL_SERVICE_VERSION")
	if serviceVersion == "" {
		serviceVersion = "1.0.0"
	}

	// Create resource with service information
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
			semconv.DeploymentEnvironment(env),
		),
	)
	if err != nil {
		return err
	}

	// Set default exporter to "none" unless explicitly configured
	exporterType := os.Getenv("OTEL_TRACES_EXPORTER")
	if exporterType == "" {
		exporterType = "none"
	}

	var exporter sdktrace.SpanExporter
	var err2 error

	switch exporterType {
	case "console":
		// Console exporter for development and debugging
		// Use pretty print for readable output, can be customized via environment
		opts := []stdouttrace.Option{stdouttrace.WithPrettyPrint()}

		// Allow customization via environment variables
		if os.Getenv("OTEL_STDOUT_WITHOUT_TIMESTAMPS") == "true" {
			opts = append(opts, stdouttrace.WithoutTimestamps())
		}

		// Allow custom writer destination
		if writerDest := os.Getenv("OTEL_STDOUT_WRITER"); writerDest != "" {
			var writer io.Writer
			switch writerDest {
			case "stderr":
				writer = os.Stderr
			case "stdout":
				writer = os.Stdout
			default:
				// Try to open as file path
				if strings.HasPrefix(writerDest, "file:") {
					filePath := strings.TrimPrefix(writerDest, "file:")
					if file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
						writer = file
					}
				}
			}
			if writer != nil {
				opts = append(opts, stdouttrace.WithWriter(writer))
			}
		}

		// Allow disabling pretty print
		if prettyPrint := os.Getenv("OTEL_STDOUT_PRETTY_PRINT"); prettyPrint == "false" {
			// Remove WithPrettyPrint and add without it
			opts = []stdouttrace.Option{}
			if os.Getenv("OTEL_STDOUT_WITHOUT_TIMESTAMPS") == "true" {
				opts = append(opts, stdouttrace.WithoutTimestamps())
			}
			if writerDest := os.Getenv("OTEL_STDOUT_WRITER"); writerDest != "" {
				var writer io.Writer
				switch writerDest {
				case "stderr":
					writer = os.Stderr
				case "stdout":
					writer = os.Stdout
				default:
					if strings.HasPrefix(writerDest, "file:") {
						filePath := strings.TrimPrefix(writerDest, "file:")
						if file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
							writer = file
						}
					}
				}
				if writer != nil {
					opts = append(opts, stdouttrace.WithWriter(writer))
				}
			}
		}

		exporter, err2 = stdouttrace.New(opts...)
	case "otlp":
		// OTLP exporter for production (sends to Grafana Alloy or other OTLP-compatible backends)
		endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		if endpoint == "" {
			endpoint = "http://grafana-alloy:4317"
		}

		// Build client options based on environment and best practices
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(endpoint),
			otlptracehttp.WithInsecure(), // For development, can be overridden
		}

		// Add timeout if specified (supports both duration strings and milliseconds)
		if timeout := os.Getenv("OTEL_EXPORTER_OTLP_TIMEOUT"); timeout != "" {
			// Try parsing as duration first (e.g., "10s", "30s")
			if duration, err := time.ParseDuration(timeout); err == nil {
				opts = append(opts, otlptracehttp.WithTimeout(duration))
			} else {
				// Try parsing as milliseconds (e.g., "10000")
				if ms, err := strconv.Atoi(timeout); err == nil {
					opts = append(opts, otlptracehttp.WithTimeout(time.Duration(ms)*time.Millisecond))
				}
			}
		}

		// Add headers if specified (useful for authentication)
		if headers := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); headers != "" {
			// Parse headers in format "key1=value1,key2=value2"
			headerMap := make(map[string]string)
			for _, pair := range strings.Split(headers, ",") {
				if kv := strings.SplitN(strings.TrimSpace(pair), "=", 2); len(kv) == 2 {
					headerMap[kv[0]] = kv[1]
				}
			}
			if len(headerMap) > 0 {
				opts = append(opts, otlptracehttp.WithHeaders(headerMap))
			}
		}

		// Add compression if specified
		if compression := os.Getenv("OTEL_EXPORTER_OTLP_COMPRESSION"); compression == "gzip" {
			opts = append(opts, otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
		}

		// Add custom URL path if specified
		if urlPath := os.Getenv("OTEL_EXPORTER_OTLP_URL_PATH"); urlPath != "" {
			opts = append(opts, otlptracehttp.WithURLPath(urlPath))
		}

		// Add retry configuration if specified
		if retryEnabled := os.Getenv("OTEL_EXPORTER_OTLP_RETRY_ENABLED"); retryEnabled == "true" {
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
		return fmt.Errorf("unsupported OpenTelemetry exporter type: %s", exporterType)
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
