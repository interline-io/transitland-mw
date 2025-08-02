// Package nginxauth provides HTTP authentication middleware for nginx's ngx_http_auth_request_module.
//
// This package implements an authentication server that validates requests using a chain of validators,
// responding with appropriate HTTP status codes for nginx to allow or deny requests. It supports:
//
//   - Unified Validator interface for extensible authentication methods
//   - API key validation through headers (see apikey.go)
//   - JWT token validation with RSA signatures (see jwt.go)
//   - Validator chaining - first successful validator wins
//   - JSON-based configuration loading for API keys
//   - Health check endpoints for monitoring
//   - Request logging for debugging and auditing
//   - X-Username header injection for downstream services
//
// The server is designed to be used with nginx's auth_request directive, where nginx
// forwards authentication requests to this service before allowing access to protected resources.
//
// Package structure:
//   - nginxauth.go: Main server implementation and HTTP handlers
//   - validator.go: Unified Validator interface
//   - apikey.go: API key validation implementation
//   - jwt.go: JWT validation implementation
//
// The unified Validator interface allows for flexible authentication:
//
//	type Validator interface {
//	    Validate(r *http.Request) (username string, valid bool, err error)
//	}
//
// Example usage:
//
//	// API Key only
//	apiKeyValidator := nginxauth.NewAPIKeyValidator()
//	apiKeyValidator.LoadConfig("api-keys.json")
//	server := nginxauth.NewServerWithValidators(config, apiKeyValidator)
//
//	// JWT only
//	jwtValidator, _ := nginxauth.NewJWTValidator(nginxauth.JWTConfig{
//		PublicKeyPath: "public-key.pem",
//		Audience:      "my-api",
//		Issuer:        "auth-server",
//	})
//	server := nginxauth.NewServerWithValidators(config, jwtValidator)
//
//	// Both API Key and JWT (API key checked first)
//	server := nginxauth.NewServerWithValidators(config, apiKeyValidator, jwtValidator)
//
//	// Add validators dynamically
//	server.AddValidator(customValidator)
//
//	mux := server.SetupRoutes()
//	http.ListenAndServe(":8080", mux)
package nginxauth

import (
	"net/http"

	"github.com/interline-io/log"
)

// Validator is the unified interface for all authentication methods.
// Validators inspect the request and return a username if authentication is successful.
type Validator interface {
	Validate(r *http.Request) (username string, valid bool, err error)
}

// ServerConfig represents server-level configuration
type ServerConfig struct {
	LogLevel       string `json:"logLevel"`
	RequestLogging bool   `json:"requestLogging"`
}

// Server handles HTTP authentication for nginx ngx_http_auth_request_module
type Server struct {
	validators     []Validator
	config         ServerConfig
	requestLogging bool
}

// NewServer creates a new auth server with default API key validator (for backward compatibility)
func NewServer() *Server {
	defaultConfig := ServerConfig{
		LogLevel:       "debug",
		RequestLogging: true,
	}
	return NewServerWithConfig(defaultConfig)
}

// NewServerWithConfig creates a new auth server with the provided configuration and default API key validator
func NewServerWithConfig(config ServerConfig) *Server {
	validator := NewAPIKeyValidator()
	return NewServerWithValidators(config, validator)
}

// NewServerWithValidators creates a new auth server with custom validators
func NewServerWithValidators(config ServerConfig, validators ...Validator) *Server {
	return &Server{
		validators:     validators,
		config:         config,
		requestLogging: config.RequestLogging,
	}
}

// AddValidator adds a validator to the server's validator chain
func (s *Server) AddValidator(validator Validator) {
	s.validators = append(s.validators, validator)
}

// Legacy constructor functions for backward compatibility

// NewServerWithValidator creates a new auth server with a custom API key validator (backward compatibility)
// Deprecated: Use NewServerWithValidators instead
func NewServerWithValidator(config ServerConfig, apiKeyValidator APIKeyValidatorInterface) *Server {
	// Create an adapter to wrap the old interface
	adapter := &legacyAPIKeyAdapter{validator: apiKeyValidator}
	return NewServerWithValidators(config, adapter)
}

// legacyAPIKeyAdapter adapts the old APIKeyValidator interface to the new Validator interface
type legacyAPIKeyAdapter struct {
	validator APIKeyValidatorInterface
}

func (a *legacyAPIKeyAdapter) Validate(r *http.Request) (string, bool, error) {
	apiKey := r.Header.Get("apikey")
	if apiKey == "" {
		return "", false, nil
	}
	return a.validator.CheckAPIKey(apiKey)
}

// SetupRoutes configures the HTTP routes for the auth server
func (s *Server) SetupRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", s.authHandler)
	mux.HandleFunc("/health", s.healthHandler)
	return mux
}

// authHandler validates requests using the configured validator chain
func (s *Server) authHandler(w http.ResponseWriter, r *http.Request) {
	// Try each validator in order until one succeeds
	for i, validator := range s.validators {
		username, valid, err := validator.Validate(r)

		if err != nil {
			if s.requestLogging {
				log.Errorf("auth request validation error from validator %d from %s: %v", i, r.RemoteAddr, err)
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if valid {
			// Set the username header for nginx to use
			w.Header().Set("X-Username", username)
			if s.requestLogging {
				log.Debugf("auth request successful with validator %d (username: %s) from %s", i, username, r.RemoteAddr)
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		// Continue to next validator if this one didn't match
	}

	// No validator succeeded
	if s.requestLogging {
		log.Debugf("auth request failed - no validator succeeded for request from %s", r.RemoteAddr)
	}
	w.WriteHeader(http.StatusForbidden)
}

// healthHandler provides a simple health check endpoint
func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
