package otel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/interline-io/transitland-mw/auth/authn"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func newTestServerWithMiddleware(middlewares ...Middleware) *httptest.Server {
	r := chi.NewRouter()
	for _, mw := range middlewares {
		r.Use(mw)
	}
	r.Get("/test-endpoint", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	})
	r.Post("/test-graphql", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": [{"test": "response"}]}`))
	})
	r.Get("/test-rest/{id}/{name}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		name := chi.URLParam(r, "name")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "` + id + `", "name": "` + name + `"}`))
	})
	return httptest.NewServer(r)
}

func flushSpans(t *testing.T) {
	if tp, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := tp.ForceFlush(ctx)
		if err != nil {
			t.Fatalf("Failed to flush spans: %v", err)
		}
	} else {
		t.Fatal("Expected TracerProvider to be of type *sdktrace.TracerProvider")
	}
}

func TestOTelHTTPMiddleware(t *testing.T) {
	mx := &mockExporter{}
	config := &Config{
		ServiceName:       "test-service",
		TracesExporter:    "mock", // Use mock exporter
		mockExporter:      mx,     // Use a mock exporter for testing
		EnableHTTPTracing: true,
	}
	err := InitSDKWithConfig("test", config)
	assert.NoError(t, err, "expected no error during SDK initialization")

	// Create a test server using the chi router
	server := newTestServerWithMiddleware(
		NewOTelHTTPMiddleware(config),
	)
	defer server.Close()

	// Make a test request to the server
	resp, err := http.Get(server.URL + "/test-endpoint")
	assert.NoError(t, err, "expected no error during request")
	defer resp.Body.Close()

	// Verify the response
	assert.Equal(t, http.StatusOK, resp.StatusCode, "expected status 200")

	// Verify that the mock exporter recorded spans
	flushSpans(t)
	if len(mx.RecordedSpans) == 0 {
		t.Error("expected mock exporter to record spans, but got none")
	} else {
		// Verify the span details
		span := mx.RecordedSpans[0]
		assert.Equal(t, "/test-endpoint", span.Name(), "expected span name '/test-endpoint'")

		// Verify some key attributes are present
		var foundHttpMethod, foundHttpRoute, foundUserAgent bool
		for _, attr := range span.Attributes() {
			switch attr.Key {
			case "http.method":
				assert.Equal(t, "GET", attr.Value.AsString(), "expected http.method to be 'GET'")
				foundHttpMethod = true
			case "http.route":
				assert.Equal(t, "/test-endpoint", attr.Value.AsString(), "expected http.route to be '/test-endpoint'")
				foundHttpRoute = true
			case "http.user_agent":
				assert.Equal(t, "Go-http-client/1.1", attr.Value.AsString(), "expected http.user_agent to be 'Go-http-client/1.1'")
				foundUserAgent = true
			}
		}
		assert.True(t, foundHttpMethod, "expected to find http.method attribute in span")
		assert.True(t, foundHttpRoute, "expected to find http.route attribute in span")
		assert.True(t, foundUserAgent, "expected to find http.user_agent attribute in span")
	}
}

func TestGraphQLHTTPMiddleware(t *testing.T) {
	mx := &mockExporter{}
	config := &Config{
		ServiceName:       "test-service",
		TracesExporter:    "mock", // Use mock exporter
		mockExporter:      mx,     // Use a mock exporter for testing
		EnableHTTPTracing: true,
	}
	err := InitSDKWithConfig("test", config)
	assert.NoError(t, err, "expected no error during SDK initialization")

	// Create a test server using the chi router
	server := newTestServerWithMiddleware(
		NewOTelHTTPMiddleware(config),
		NewGraphQLHTTPMiddleware(config),
	)
	defer server.Close()

	// Make a test request to the server
	path := "/test-graphql"
	resp, err := http.Post(server.URL+path, "application/json", nil)
	assert.NoError(t, err, "expected no error during request")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "expected status 200")

	// Verify that the mock exporter recorded spans
	flushSpans(t)
	if len(mx.RecordedSpans) == 0 {
		t.Error("expected mock exporter to record spans, but got none")
	} else {
		// Verify the span details
		span := mx.RecordedSpans[0]
		assert.Equalf(t, path, span.Name(), "expected span name '%s'", path)

		// Verify some key attributes are present
		attrs := span.Attributes()
		var foundOperationType, foundApiType bool
		for _, attr := range attrs {
			switch attr.Key {
			case "graphql.request_type":
				assert.Equal(t, "operation", attr.Value.AsString(), "expected graphql.request_type to be 'operation'")
				foundOperationType = true
			case "api.type":
				assert.Equal(t, "graphql", attr.Value.AsString(), "expected api.type to be 'graphql'")
				foundApiType = true
			}
		}
		assert.True(t, foundOperationType, "expected to find graphql.request_type attribute in span")
		assert.True(t, foundApiType, "expected to find api.type attribute in span")
	}
}

func TestRestHTTPMiddleware(t *testing.T) {
	mx := &mockExporter{}
	config := &Config{
		ServiceName:       "test-service",
		TracesExporter:    "mock", // Use mock exporter
		mockExporter:      mx,     // Use a mock exporter for testing
		EnableHTTPTracing: true,
	}
	err := InitSDKWithConfig("test", config)
	assert.NoError(t, err, "expected no error during SDK initialization")

	// Create a test server using the chi router
	server := newTestServerWithMiddleware(
		NewOTelHTTPMiddleware(config),
		NewRestHTTPMiddleware(config),
	)
	defer server.Close()

	// Make a test request to the server
	path := "/test-rest/123/abc?test=ok"
	handlerName := "/test-rest/{id}/{name}"
	resp, err := http.Get(server.URL + path)
	assert.NoError(t, err, "expected no error during request")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "expected status 200")

	// Verify that the mock exporter recorded spans
	flushSpans(t)
	if len(mx.RecordedSpans) == 0 {
		t.Error("expected mock exporter to record spans, but got none")
	} else {
		// Verify the span details
		span := mx.RecordedSpans[0]
		assert.Equalf(t, handlerName, span.Name(), "expected span name '%s'", path)

		// Verify some key attributes are present
		attrs := span.Attributes()
		var foundApiType, foundQueryParam, foundPathId bool
		for _, attr := range attrs {
			switch attr.Key {
			case "api.type":
				assert.Equal(t, "rest", attr.Value.AsString(), "expected api.type to be 'rest'")
				foundApiType = true
			case "http.query_param.test":
				assert.Equal(t, "ok", attr.Value.AsString(), "expected http.query_param.test to be 'ok'")
				foundQueryParam = true
			case "http.path_param.id":
				assert.Equal(t, "123", attr.Value.AsString(), "expected http.path_param.id to be '123'")
				foundPathId = true
			default:
			}
		}
		assert.True(t, foundApiType, "expected to find api.type attribute in span")
		assert.True(t, foundQueryParam, "expected to find http.query_param.test attribute in span")
		assert.True(t, foundPathId, "expected to find http.path_param.id attribute in span")
	}
}

func TestUserMiddleware(t *testing.T) {
	mx := &mockExporter{}
	config := &Config{
		ServiceName:       "test-service",
		TracesExporter:    "mock", // Use mock exporter
		mockExporter:      mx,     // Use a mock exporter for testing
		EnableHTTPTracing: true,
	}
	err := InitSDKWithConfig("test", config)
	assert.NoError(t, err, "expected no error during SDK initialization")

	// Create a test server using the helper function
	server := newTestServerWithMiddleware(
		NewOTelHTTPMiddleware(config),
		NewUserHTTPMiddleware(config),
	)
	defer server.Close()

	// Create a request with a mock user in the context
	path := "/test-endpoint"
	rctx := authn.WithUser(context.Background(), authn.NewCtxUser("ian", "", ""))
	req := httptest.NewRequest("GET", path, nil)
	req = req.WithContext(rctx)

	// Use ResponseRecorder and call ServeHTTP directly on the server's handler
	rr := httptest.NewRecorder()
	server.Config.Handler.ServeHTTP(rr, req)

	// Verify the response
	assert.Equal(t, http.StatusOK, rr.Code, "expected status 200")

	// Verify that the mock exporter recorded spans
	flushSpans(t)
	if len(mx.RecordedSpans) == 0 {
		t.Error("expected mock exporter to record spans, but got none")
	} else {
		// Verify the span details
		span := mx.RecordedSpans[0]
		assert.Equalf(t, path, span.Name(), "expected span name '%s'", path)

		// Verify some key attributes are present
		attrs := span.Attributes()
		var foundUserId bool
		for _, attr := range attrs {
			switch attr.Key {
			case "user.id":
				assert.Equal(t, "ian", attr.Value.AsString(), "expected user.id to be 'ian'")
				foundUserId = true
			default:
			}
		}
		assert.True(t, foundUserId, "expected to find user.id attribute in span")
	}
}
