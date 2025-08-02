// Package nginxauth provides HTTP authentication middleware for nginx's ngx_http_auth_request_module.
//
// This package implements an authentication server that validates API keys and responds with
// appropriate HTTP status codes for nginx to allow or deny requests. It supports:
//
//   - Configurable API key validation through the APIKeyValidator interface
//   - JSON-based configuration loading for API keys
//   - Health check endpoints for monitoring
//   - Request logging for debugging and auditing
//
// The server is designed to be used with nginx's auth_request directive, where nginx
// forwards authentication requests to this service before allowing access to protected resources.
package nginxauth

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/interline-io/log"
)

// APIKeyConfig represents configuration for a single API key
type APIKeyConfig struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// APIKeyValidator interface allows for custom API key validation implementations
type APIKeyValidator interface {
	CheckAPIKey(apiKey string) (bool, error)
}

// ConfigBasedValidator implements APIKeyValidator using a configuration-based approach
type ConfigBasedValidator struct {
	validAPIKeys map[string]bool
}

func NewConfigBasedValidator() *ConfigBasedValidator {
	return &ConfigBasedValidator{
		validAPIKeys: make(map[string]bool),
	}
}

// CheckAPIKey validates an API key against the configured valid keys
func (v *ConfigBasedValidator) CheckAPIKey(apiKey string) (bool, error) {
	return v.validAPIKeys[apiKey], nil
}

func (v *ConfigBasedValidator) LoadConfig(path string) error {
	v.validAPIKeys = make(map[string]bool)
	file, err := os.Open(path)
	if err != nil {
		log.Errorf("Failed to open API key config file %s: %v", path, err)
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		log.Errorf("Failed to read API key config file %s: %v", path, err)
		return err
	}
	var apiKeys []APIKeyConfig
	if err := json.Unmarshal(data, &apiKeys); err != nil {
		log.Errorf("Failed to parse API key config JSON: %v", err)
		return err
	}
	for _, key := range apiKeys {
		if key.Enabled {
			v.validAPIKeys[key.Name] = true
			log.Infof("Loaded API key: %s", key.Name)
		} else {
			log.Infof("Disabled API key: %s", key.Name)
		}
	}
	return nil
}

// ServerConfig represents server-level configuration
type ServerConfig struct {
	LogLevel       string `json:"logLevel"`
	RequestLogging bool   `json:"requestLogging"`
}

// Server handles HTTP authentication for nginx ngx_http_auth_request_module
type Server struct {
	validator      APIKeyValidator
	config         ServerConfig
	requestLogging bool
}

// NewServer creates a new auth server with default API keys (for backward compatibility)
func NewServer() *Server {
	defaultConfig := ServerConfig{
		LogLevel:       "debug",
		RequestLogging: true,
	}
	return NewServerWithConfig(defaultConfig)
}

// NewServerWithConfig creates a new auth server with the provided configuration
func NewServerWithConfig(config ServerConfig) *Server {
	validator := NewConfigBasedValidator()
	return NewServerWithValidator(config, validator)
}

// NewServerWithValidator creates a new auth server with a custom validator
func NewServerWithValidator(config ServerConfig, validator APIKeyValidator) *Server {
	return &Server{
		validator:      validator,
		config:         config,
		requestLogging: config.RequestLogging,
	}
}

// SetupRoutes configures the HTTP routes for the auth server
func (s *Server) SetupRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", s.authHandler)
	mux.HandleFunc("/health", s.healthHandler)
	return mux
}

// authHandler validates API keys for nginx auth_request module
func (s *Server) authHandler(w http.ResponseWriter, r *http.Request) {
	apiKey := r.Header.Get("apikey")

	if apiKey == "" {
		if s.requestLogging {
			log.Debugf("auth request missing apikey header from %s", r.RemoteAddr)
		}
		w.WriteHeader(http.StatusForbidden)
		return
	}

	valid, err := s.validator.CheckAPIKey(apiKey)
	if err != nil {
		if s.requestLogging {
			log.Errorf("auth request validation error for key %s from %s: %v", apiKey, r.RemoteAddr, err)
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if valid {
		if s.requestLogging {
			log.Debugf("auth request successful for key %s from %s", apiKey, r.RemoteAddr)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if s.requestLogging {
		log.Debugf("auth request failed for invalid key %s from %s", apiKey, r.RemoteAddr)
	}
	w.WriteHeader(http.StatusForbidden)
}

// healthHandler provides a simple health check endpoint
func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
