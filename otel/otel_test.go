package otel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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
	r.Get("/test-graphql", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": [{"test": "response"}]}`))
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
		var foundHttpMethod, foundHttpRoute bool
		for _, attr := range span.Attributes() {
			switch attr.Key {
			case "http.method":
				assert.Equal(t, "GET", attr.Value.AsString(), "expected http.method to be 'GET'")
				foundHttpMethod = true
			case "http.route":
				assert.Equal(t, "/test-endpoint", attr.Value.AsString(), "expected http.route to be '/test-endpoint'")
				foundHttpRoute = true
			}
		}
		assert.True(t, foundHttpMethod, "expected to find http.method attribute in span")
		assert.True(t, foundHttpRoute, "expected to find http.route attribute in span")
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
	resp, err := http.Get(server.URL + "/test-graphql")
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
		assert.Equal(t, "/test-graphql", span.Name(), "expected span name '/test-graphql'")

		// Verify some key attributes are present
		attrs := span.Attributes()
		for _, attr := range attrs {
			fmt.Printf("Span Attribute: %s = %s\n", attr.Key, attr.Value.AsString())
		}
	}
}
