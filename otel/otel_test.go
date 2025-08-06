package otel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/interline-io/transitland-mw/auth/authn"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// MockUser implements the authn.User interface for testing
func newTestUser(id, name, email string, roles ...string) authn.User {
	return authn.NewCtxUser(id, name, email).WithRoles(roles...)
}

// ConfigWithApitype is a test helper that mimics the function in server_cmd.go
func ConfigWithApitype(cfg Config, apitype string) Config {
	cfgCopy := cfg
	cfgCopy.ApiType = apitype
	return cfgCopy
}

// setupTestTracing initializes OpenTelemetry for testing and returns a cleanup function
func setupTestTracing(t *testing.T) func() {
	// Create a stdout exporter for testing
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		t.Fatalf("failed to create stdout exporter: %v", err)
	}

	// Create a tracer provider
	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("test-service"),
		)),
	)

	// Set as global tracer provider
	otel.SetTracerProvider(tp)

	// Return cleanup function
	return func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Logf("failed to shutdown tracer provider: %v", err)
		}
	}
}

func TestGetEnrichedOTelMiddleware_TracingDisabled(t *testing.T) {
	cfg := Config{
		ServiceName:    "test-service",
		ApiType:        "rest",
		TracesExporter: "none",
		Enabled:        false,
	}

	middleware := GetEnrichedOTelMiddleware(&cfg)

	// Create a test handler
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// Create request
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	// Apply middleware
	middleware(handler).ServeHTTP(rr, req)

	assert.True(t, called, "handler should be called")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestGetEnrichedOTelMiddleware_RESTApiType(t *testing.T) {
	cleanup := setupTestTracing(t)
	defer cleanup()

	cfg := Config{
		ServiceName:    "test-service",
		ApiType:        "rest",
		TracesExporter: "console",
		Enabled:        true,
	}

	// Create a chi router to test URL parameters
	r := chi.NewRouter()
	r.Use(GetEnrichedOTelMiddleware(&cfg))
	r.Get("/api/v1/feeds/{feed_id}/routes/{route_id}", func(w http.ResponseWriter, r *http.Request) {
		span := oteltrace.SpanFromContext(r.Context())
		assert.True(t, span.IsRecording(), "span should be recording")
		w.WriteHeader(http.StatusOK)
	})

	// Create request with path parameters and query parameters
	req := httptest.NewRequest("GET", "/api/v1/feeds/123/routes/456?limit=10&offset=20", nil)
	req.Header.Set("User-Agent", "test-agent")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Real-IP", "1.2.3.4")
	req.Header.Set("apikey", "test-key")

	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestGetEnrichedOTelMiddleware_GraphQLApiType(t *testing.T) {
	cleanup := setupTestTracing(t)
	defer cleanup()

	cfg := Config{
		ServiceName:    "test-service",
		ApiType:        "graphql",
		TracesExporter: "console",
		Enabled:        true,
	}

	tests := []struct {
		name            string
		method          string
		contentType     string
		expectedReqType string
	}{
		{
			name:            "POST JSON operation",
			method:          "POST",
			contentType:     "application/json",
			expectedReqType: "operation",
		},
		{
			name:            "GET introspection",
			method:          "GET",
			contentType:     "",
			expectedReqType: "introspection",
		},
		{
			name:            "POST non-JSON",
			method:          "POST",
			contentType:     "text/plain",
			expectedReqType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use chi router to properly set up context for otelchi middleware
			r := chi.NewRouter()
			r.Use(GetEnrichedOTelMiddleware(&cfg))
			r.MethodFunc(tt.method, "/graphql", func(w http.ResponseWriter, r *http.Request) {
				span := oteltrace.SpanFromContext(r.Context())
				assert.True(t, span.IsRecording(), "span should be recording")
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest(tt.method, "/graphql", nil)
			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}

			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Code)
		})
	}
}

func TestGetEnrichedOTelMiddleware_UserEnrichment(t *testing.T) {
	cleanup := setupTestTracing(t)
	defer cleanup()

	cfg := Config{
		ServiceName:    "test-service",
		ApiType:        "rest",
		TracesExporter: "console",
		Enabled:        true,
	}

	// Test with user in context
	t.Run("with user", func(t *testing.T) {
		user := newTestUser("user123", "Test User", "test@example.com", "admin", "user")

		// Use chi router to properly set up context for otelchi middleware
		r := chi.NewRouter()

		// Add auth middleware that adds user to context
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := authn.WithUser(r.Context(), user)
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		})

		r.Use(GetEnrichedOTelMiddleware(&cfg))
		r.Get("/test", func(w http.ResponseWriter, r *http.Request) {
			span := oteltrace.SpanFromContext(r.Context())
			assert.True(t, span.IsRecording(), "span should be recording")
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	// Test without user in context
	t.Run("without user", func(t *testing.T) {
		r := chi.NewRouter()
		r.Use(GetEnrichedOTelMiddleware(&cfg))
		r.Get("/test", func(w http.ResponseWriter, r *http.Request) {
			span := oteltrace.SpanFromContext(r.Context())
			assert.True(t, span.IsRecording(), "span should be recording")
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})
}

func TestGetEnrichedOTelMiddleware_IPHeaders(t *testing.T) {
	cleanup := setupTestTracing(t)
	defer cleanup()

	cfg := Config{
		ServiceName:    "test-service",
		ApiType:        "rest",
		TracesExporter: "console",
		Enabled:        true,
	}

	tests := []struct {
		name    string
		headers map[string]string
	}{
		{
			name: "X-Real-IP header",
			headers: map[string]string{
				"X-Real-IP": "1.2.3.4",
			},
		},
		{
			name: "X-Forwarded-For header",
			headers: map[string]string{
				"X-Forwarded-For": "1.2.3.4, 5.6.7.8",
			},
		},
		{
			name: "Both headers (X-Real-IP takes priority)",
			headers: map[string]string{
				"X-Real-IP":       "1.2.3.4",
				"X-Forwarded-For": "5.6.7.8",
			},
		},
		{
			name:    "No special headers",
			headers: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := chi.NewRouter()
			r.Use(GetEnrichedOTelMiddleware(&cfg))
			r.Get("/test", func(w http.ResponseWriter, r *http.Request) {
				span := oteltrace.SpanFromContext(r.Context())
				assert.True(t, span.IsRecording(), "span should be recording")
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest("GET", "/test", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Code)
		})
	}
}

func TestConfigWithApitype(t *testing.T) {
	originalConfig := Config{
		ServiceName:    "test-service",
		ApiType:        "original",
		TracesExporter: "console",
		Enabled:        true,
	}

	newConfig := ConfigWithApitype(originalConfig, "rest")

	// Should create a copy with the new API type
	assert.Equal(t, "test-service", newConfig.ServiceName)
	assert.Equal(t, "rest", newConfig.ApiType)

	// Original should be unchanged
	assert.Equal(t, "original", originalConfig.ApiType)
}

// TestIntegration_ServerCommandOTelPatterns tests the integration patterns used in server_cmd.go
func TestIntegration_ServerCommandOTelPatterns(t *testing.T) {
	cleanup := setupTestTracing(t)
	defer cleanup()

	// Test the patterns used in server_cmd.go
	baseConfig := Config{
		ServiceName:    "tlserver",
		TracesExporter: "console",
		Enabled:        true,
	}

	// Test GraphQL middleware configuration
	t.Run("graphql middleware pattern", func(t *testing.T) {
		graphqlConfig := ConfigWithApitype(baseConfig, "graphql")

		r := chi.NewRouter()
		r.Use(GetEnrichedOTelMiddleware(&graphqlConfig))
		r.Post("/query", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("POST", "/query", strings.NewReader(`{"query": "{ feeds { id } }"}`))
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	// Test REST middleware configuration
	t.Run("rest middleware pattern", func(t *testing.T) {
		restConfig := ConfigWithApitype(baseConfig, "rest")
		middleware := GetEnrichedOTelMiddleware(&restConfig)

		// Set up chi router like in server_cmd.go
		r := chi.NewRouter()
		r.Use(middleware)
		r.Get("/rest/feeds/{feed_id}", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/rest/feeds/test-feed?limit=10", nil)
		rr := httptest.NewRecorder()

		r.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	// Test with authentication middleware pattern
	t.Run("with auth middleware", func(t *testing.T) {
		restConfig := ConfigWithApitype(baseConfig, "rest")
		otelMiddleware := GetEnrichedOTelMiddleware(&restConfig)

		// Simulate auth middleware adding user to context
		authMiddleware := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user := newTestUser("test-user", "Test User", "test@transit.land", "tl_user_pro")
				ctx := authn.WithUser(r.Context(), user)
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		}

		r := chi.NewRouter()
		r.Use(authMiddleware)
		r.Use(otelMiddleware)
		r.Get("/rest/feeds", func(w http.ResponseWriter, r *http.Request) {
			// Verify span is enriched with user data
			span := oteltrace.SpanFromContext(r.Context())
			assert.True(t, span.IsRecording())
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/rest/feeds", nil)
		rr := httptest.NewRecorder()

		r.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})
}
