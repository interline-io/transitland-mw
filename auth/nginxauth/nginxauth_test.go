package nginxauth

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// MockAPIKeyValidator is a test implementation of the Validator interface for API keys
type MockAPIKeyValidator struct {
	validKeys    map[string]string // maps API key to username
	shouldError  bool
	errorMessage string
}

func (m *MockAPIKeyValidator) Validate(r *http.Request) (string, bool, error) {
	apiKey := r.Header.Get("apikey")
	if apiKey == "" {
		return "", false, nil // No API key present
	}

	if m.shouldError {
		return "", false, errors.New(m.errorMessage)
	}
	username, exists := m.validKeys[apiKey]
	return username, exists, nil
}

// Legacy method for backward compatibility testing
func (m *MockAPIKeyValidator) CheckAPIKey(apiKey string) (string, bool, error) {
	if m.shouldError {
		return "", false, errors.New(m.errorMessage)
	}
	username, exists := m.validKeys[apiKey]
	return username, exists, nil
}

// MockJWTValidator is a test implementation of the Validator interface for JWTs
type MockJWTValidator struct {
	validTokens  map[string]string // maps JWT token to username
	shouldError  bool
	errorMessage string
}

func (m *MockJWTValidator) Validate(r *http.Request) (string, bool, error) {
	authHeader := r.Header.Get("authorization")
	if authHeader == "" {
		return "", false, nil // No authorization header present
	}

	// Extract token from "Bearer <token>" format
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == authHeader {
		return "", false, nil // Invalid format
	}

	if m.shouldError {
		return "", false, errors.New(m.errorMessage)
	}
	username, exists := m.validTokens[token]
	return username, exists, nil
}

// Legacy method for backward compatibility testing
func (m *MockJWTValidator) CheckJWT(token string) (string, bool, error) {
	if m.shouldError {
		return "", false, errors.New(m.errorMessage)
	}
	username, exists := m.validTokens[token]
	return username, exists, nil
}

// getTestAPIKeyValidator returns a preconfigured MockAPIKeyValidator for testing
func getTestAPIKeyValidator() *MockAPIKeyValidator {
	return &MockAPIKeyValidator{
		validKeys: map[string]string{
			"dev-key-123":     "dev-user",
			"staging-key-456": "staging-user",
			"prod-key-789":    "prod-user",
			"admin-key-000":   "admin-user",
			// Note: disabled-key-999 is intentionally not included (simulating disabled key)
		},
		shouldError: false,
	}
}

// Legacy function for backward compatibility
func getTestValidator() *MockAPIKeyValidator {
	return getTestAPIKeyValidator()
}

// TestNginxAuth tests the nginx auth server behavior using a MockValidator
// This approach isolates the server logic testing from configuration file dependencies
func TestNginxAuth(t *testing.T) {
	validator := getTestValidator()
	authServer := NewServerWithValidators(ServerConfig{}, validator)
	mux := authServer.SetupRoutes()

	server := httptest.NewServer(mux)
	defer server.Close()

	t.Run("health_endpoint", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/health")
		if err != nil {
			t.Fatalf("Failed to connect to health endpoint: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected health endpoint to return 200, got %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("Failed to read response body: %v", err)
		}

		if string(body) != "OK" {
			t.Errorf("Expected health endpoint to return 'OK', got %q", string(body))
		}

		// Test different HTTP methods on health endpoint
		t.Run("http_methods", func(t *testing.T) {
			methods := []string{"POST", "PUT", "DELETE", "PATCH"}

			for _, method := range methods {
				t.Run(method, func(t *testing.T) {
					req, _ := http.NewRequest(method, server.URL+"/health", nil)

					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatalf("Failed to connect to health endpoint with %s: %v", method, err)
					}
					defer resp.Body.Close()

					if resp.StatusCode != http.StatusOK {
						t.Errorf("Expected health endpoint with %s method to return 200, got %d", method, resp.StatusCode)
					}
				})
			}
		})
	})

	t.Run("auth", func(t *testing.T) {
		t.Run("valid_keys", func(t *testing.T) {
			testCases := []struct {
				apiKey   string
				username string
			}{
				{"dev-key-123", "dev-user"},
				{"staging-key-456", "staging-user"},
				{"prod-key-789", "prod-user"},
				{"admin-key-000", "admin-user"},
			}

			for _, tc := range testCases {
				t.Run(tc.apiKey, func(t *testing.T) {
					req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
					req.Header.Set("apikey", tc.apiKey)

					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatalf("Failed to connect to auth endpoint: %v", err)
					}
					defer resp.Body.Close()

					if resp.StatusCode != http.StatusOK {
						t.Errorf("Expected auth endpoint with valid key %s to return 200, got %d", tc.apiKey, resp.StatusCode)
					}

					// Check that X-Username header is set correctly
					username := resp.Header.Get("X-Username")
					if username != tc.username {
						t.Errorf("Expected X-Username header to be %s, got %s", tc.username, username)
					}
				})
			}
		})

		t.Run("invalid_key", func(t *testing.T) {
			req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
			req.Header.Set("apikey", "invalid-key")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Failed to connect to auth endpoint: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("Expected auth endpoint with invalid key to return 403, got %d", resp.StatusCode)
			}
		})

		t.Run("disabled_key", func(t *testing.T) {
			req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
			req.Header.Set("apikey", "disabled-key-999")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Failed to connect to auth endpoint: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("Expected auth endpoint with disabled key to return 403, got %d", resp.StatusCode)
			}
		})

		t.Run("missing_key", func(t *testing.T) {
			resp, err := http.Get(server.URL + "/auth")
			if err != nil {
				t.Fatalf("Failed to connect to auth endpoint: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("Expected auth endpoint without key to return 403, got %d", resp.StatusCode)
			}
		})

		t.Run("empty_key", func(t *testing.T) {
			req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
			req.Header.Set("apikey", "")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Failed to connect to auth endpoint: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("Expected auth endpoint with empty key to return 403, got %d", resp.StatusCode)
			}
		})

		t.Run("whitespace_key", func(t *testing.T) {
			req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
			req.Header.Set("apikey", "   ")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Failed to connect to auth endpoint: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("Expected auth endpoint with whitespace key to return 403, got %d", resp.StatusCode)
			}
		})

		t.Run("case_insensitive_header", func(t *testing.T) {
			headerVariations := []string{"apikey", "Apikey", "APIKEY", "ApiKey"}

			for _, header := range headerVariations {
				t.Run(header, func(t *testing.T) {
					req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
					req.Header.Set(header, "dev-key-123")

					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatalf("Failed to connect to auth endpoint: %v", err)
					}
					defer resp.Body.Close()

					if resp.StatusCode != http.StatusOK {
						t.Errorf("Expected auth endpoint with header %s to return 200, got %d", header, resp.StatusCode)
					}
				})
			}
		})

		t.Run("http_methods", func(t *testing.T) {
			methods := []string{"POST", "PUT", "DELETE", "PATCH"}

			for _, method := range methods {
				t.Run(method, func(t *testing.T) {
					req, _ := http.NewRequest(method, server.URL+"/auth", nil)
					req.Header.Set("apikey", "dev-key-123")

					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatalf("Failed to connect to auth endpoint with %s: %v", method, err)
					}
					defer resp.Body.Close()

					if resp.StatusCode != http.StatusOK {
						t.Errorf("Expected auth endpoint with %s method to return 200, got %d", method, resp.StatusCode)
					}
				})
			}
		})
	})

	t.Run("404_handling", func(t *testing.T) {
		nonExistentPaths := []string{"/nonexistent", "/api", "/auth/extra", "/health/status"}

		for _, path := range nonExistentPaths {
			t.Run(path, func(t *testing.T) {
				resp, err := http.Get(server.URL + path)
				if err != nil {
					t.Fatalf("Failed to connect to endpoint %s: %v", path, err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusNotFound {
					t.Errorf("Expected endpoint %s to return 404, got %d", path, resp.StatusCode)
				}
			})
		}
	})
}

// TestValidatorInterface tests the APIKeyValidator interface functionality
func TestValidatorInterface(t *testing.T) {
	t.Run("custom_validator", func(t *testing.T) {
		// Create a mock validator with custom logic
		mockValidator := &MockAPIKeyValidator{
			validKeys: map[string]string{
				"custom-key-1": "custom-user-1",
				"custom-key-2": "custom-user-2",
			},
			shouldError: false,
		}

		config := ServerConfig{
			LogLevel:       "debug",
			RequestLogging: false,
		}

		authServer := NewServerWithValidators(config, mockValidator)
		mux := authServer.SetupRoutes()

		server := httptest.NewServer(mux)
		defer server.Close()

		// Test valid custom key
		req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
		req.Header.Set("apikey", "custom-key-1")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to connect to auth endpoint: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected auth endpoint with custom valid key to return 200, got %d", resp.StatusCode)
		}

		// Check that X-Username header is set correctly
		username := resp.Header.Get("X-Username")
		if username != "custom-user-1" {
			t.Errorf("Expected X-Username header to be 'custom-user-1', got %s", username)
		}

		// Test invalid custom key
		req, _ = http.NewRequest("GET", server.URL+"/auth", nil)
		req.Header.Set("apikey", "invalid-custom-key")

		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to connect to auth endpoint: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected auth endpoint with invalid custom key to return 403, got %d", resp.StatusCode)
		}
	})

	t.Run("validator_error_handling", func(t *testing.T) {
		// Create a mock validator that returns errors
		mockValidator := &MockAPIKeyValidator{
			validKeys:    map[string]string{},
			shouldError:  true,
			errorMessage: "external service unavailable",
		}

		config := ServerConfig{
			LogLevel:       "debug",
			RequestLogging: false,
		}

		authServer := NewServerWithValidators(config, mockValidator)
		mux := authServer.SetupRoutes()

		server := httptest.NewServer(mux)
		defer server.Close()

		req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
		req.Header.Set("apikey", "any-key")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to connect to auth endpoint: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("Expected auth endpoint with validator error to return 500, got %d", resp.StatusCode)
		}
	})
}

// TestJWTAuth tests JWT authentication functionality
func TestJWTAuth(t *testing.T) {
	jwtValidator := &MockJWTValidator{
		validTokens: map[string]string{
			"valid-jwt-token-1": "jwt-user-1",
			"valid-jwt-token-2": "jwt-user-2",
		},
		shouldError: false,
	}

	config := ServerConfig{
		LogLevel:       "debug",
		RequestLogging: false,
	}

	authServer := NewServerWithValidators(config, jwtValidator)
	mux := authServer.SetupRoutes()

	server := httptest.NewServer(mux)
	defer server.Close()

	t.Run("valid_jwt", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
		req.Header.Set("authorization", "Bearer valid-jwt-token-1")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to connect to auth endpoint: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected auth endpoint with valid JWT to return 200, got %d", resp.StatusCode)
		}

		// Check that X-Username header is set correctly
		username := resp.Header.Get("X-Username")
		if username != "jwt-user-1" {
			t.Errorf("Expected X-Username header to be 'jwt-user-1', got %s", username)
		}
	})

	t.Run("invalid_jwt", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
		req.Header.Set("authorization", "Bearer invalid-jwt-token")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to connect to auth endpoint: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected auth endpoint with invalid JWT to return 403, got %d", resp.StatusCode)
		}
	})

	t.Run("malformed_authorization_header", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
		req.Header.Set("authorization", "valid-jwt-token-1") // Missing "Bearer " prefix

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to connect to auth endpoint: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected auth endpoint with malformed authorization header to return 403, got %d", resp.StatusCode)
		}
	})

	t.Run("jwt_validator_error", func(t *testing.T) {
		errorJWTValidator := &MockJWTValidator{
			validTokens:  map[string]string{},
			shouldError:  true,
			errorMessage: "JWT validation service down",
		}

		authServer := NewServerWithValidators(config, errorJWTValidator)
		mux := authServer.SetupRoutes()
		server := httptest.NewServer(mux)
		defer server.Close()

		req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
		req.Header.Set("authorization", "Bearer any-token")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to connect to auth endpoint: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("Expected auth endpoint with JWT validator error to return 500, got %d", resp.StatusCode)
		}
	})
}

// TestMixedAuth tests scenarios with both API key and JWT validators
func TestMixedAuth(t *testing.T) {
	apiKeyValidator := getTestValidator()
	jwtValidator := &MockJWTValidator{
		validTokens: map[string]string{
			"jwt-token": "jwt-user",
		},
		shouldError: false,
	}

	config := ServerConfig{
		LogLevel:       "debug",
		RequestLogging: false,
	}

	authServer := NewServerWithValidators(config, apiKeyValidator, jwtValidator)
	mux := authServer.SetupRoutes()

	server := httptest.NewServer(mux)
	defer server.Close()

	t.Run("api_key_takes_precedence", func(t *testing.T) {
		// When both headers are present, API key should take precedence
		req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
		req.Header.Set("apikey", "dev-key-123")
		req.Header.Set("authorization", "Bearer jwt-token")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to connect to auth endpoint: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected auth endpoint to return 200, got %d", resp.StatusCode)
		}

		// Should get username from API key validator
		username := resp.Header.Get("X-Username")
		if username != "dev-user" {
			t.Errorf("Expected X-Username header to be 'dev-user' (from API key), got %s", username)
		}
	})

	t.Run("jwt_when_no_api_key", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
		req.Header.Set("authorization", "Bearer jwt-token")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to connect to auth endpoint: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected auth endpoint to return 200, got %d", resp.StatusCode)
		}

		// Should get username from JWT validator
		username := resp.Header.Get("X-Username")
		if username != "jwt-user" {
			t.Errorf("Expected X-Username header to be 'jwt-user' (from JWT), got %s", username)
		}
	})

	t.Run("no_validator_configured", func(t *testing.T) {
		// Test with only JWT validator configured but API key provided
		authServerJWTOnly := NewServerWithValidators(config, jwtValidator)
		muxJWTOnly := authServerJWTOnly.SetupRoutes()
		serverJWTOnly := httptest.NewServer(muxJWTOnly)
		defer serverJWTOnly.Close()

		req, _ := http.NewRequest("GET", serverJWTOnly.URL+"/auth", nil)
		req.Header.Set("apikey", "dev-key-123")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to connect to auth endpoint: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected auth endpoint with API key but no validator to return 403, got %d", resp.StatusCode)
		}
	})
}
