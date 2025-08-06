package otel

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/interline-io/transitland-mw/auth/authn"
	"github.com/riandyrn/otelchi"
	"github.com/riverqueue/rivercontrib/otelriver"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Middleware Functions
type Middleware func(next http.Handler) http.Handler

func NewOTelHTTPMiddleware(config *Config) Middleware {
	return func(next http.Handler) http.Handler {
		baseMw := otelchi.Middleware(config.ServiceName)
		return baseMw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			span := trace.SpanFromContext(r.Context())
			span.SetAttributes(attribute.String("http.user_agent", r.UserAgent()))

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

			// Add request ID if set
			if requestID := middleware.GetReqID(r.Context()); requestID != "" {
				span.SetAttributes(attribute.String("request.id", requestID))
			}

			next.ServeHTTP(w, r)
		}))
	}
}

// NewUserHTTPMiddleware creates an HTTP OpenTelemetry middleware for user requests.
func NewUserHTTPMiddleware(config *Config) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

			// Just mark if an API key is present, never include the actual key
			if apiKey := r.Header.Get("apikey"); apiKey != "" {
				span.SetAttributes(attribute.String("http.apikey", "present"))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func NewGraphQLHTTPMiddleware(cfg *Config) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			span := trace.SpanFromContext(r.Context())
			// For GraphQL, check if it's a POST with JSON body
			span.SetAttributes(attribute.String("api.type", "graphql"))
			if r.Method == "POST" && r.Header.Get("Content-Type") == "application/json" {
				// We'll only track that it's a GraphQL operation
				span.SetAttributes(attribute.String("graphql.request_type", "operation"))
			} else if r.Method == "GET" {
				// GET requests to GraphQL endpoint are usually schema introspection
				span.SetAttributes(attribute.String("graphql.request_type", "introspection"))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func NewRestHTTPMiddleware(cfg *Config) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			span := trace.SpanFromContext(r.Context())
			// Add URL parameters from chi router context
			span.SetAttributes(attribute.String("api.type", "rest"))
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
			next.ServeHTTP(w, r)
		})
	}
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
