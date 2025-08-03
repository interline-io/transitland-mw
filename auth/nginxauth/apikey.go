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
	Username    string `json:"username"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// APIKeyValidator implements the Validator interface for API key authentication.
// It checks for API keys in the "apikey" header and validates them against a configured set.
type APIKeyValidator struct {
	validAPIKeys map[string]string // maps API key to username
}

// NewAPIKeyValidator creates a new API key validator
func NewAPIKeyValidator() *APIKeyValidator {
	return &APIKeyValidator{
		validAPIKeys: make(map[string]string),
	}
}

// Validate implements the Validator interface by checking for API keys in the request headers
func (v *APIKeyValidator) Validate(r *http.Request) (string, bool, error) {
	apiKey := r.Header.Get("apikey")
	if apiKey == "" {
		return "", false, nil // No API key present, let other validators try
	}

	username, exists := v.validAPIKeys[apiKey]
	return username, exists, nil
}

// LoadConfig loads API key configuration from a JSON file.
// The JSON should contain an array of APIKeyConfig objects with name, username, description, and enabled fields.
// If no username is specified, the key name will be used as the username.
func (v *APIKeyValidator) LoadConfig(path string) error {
	v.validAPIKeys = make(map[string]string)
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
			username := key.Username
			if username == "" {
				username = key.Name // fallback to key name if no username specified
			}
			v.validAPIKeys[key.Name] = username
			log.Infof("Loaded API key: %s (username: %s)", key.Name, username)
		} else {
			log.Infof("Disabled API key: %s", key.Name)
		}
	}
	return nil
}

// Legacy interfaces and types for backward compatibility

// APIKeyValidator interface allows for custom API key validation implementations
// Deprecated: Use the unified Validator interface instead
type APIKeyValidatorInterface interface {
	CheckAPIKey(apiKey string) (string, bool, error)
}
