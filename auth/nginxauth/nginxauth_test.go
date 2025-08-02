package nginxauth

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// MockValidator is a test implementation of APIKeyValidator
// This allows us to test the server logic without depending on external configuration files
type MockValidator struct {
	validKeys    map[string]bool
	shouldError  bool
	errorMessage string
}

func (m *MockValidator) CheckAPIKey(apiKey string) (bool, error) {
	if m.shouldError {
		return false, errors.New(m.errorMessage)
	}
	return m.validKeys[apiKey], nil
}

// getTestValidator returns a preconfigured MockValidator for testing
func getTestValidator() *MockValidator {
	return &MockValidator{
		validKeys: map[string]bool{
			"dev-key-123":     true,
			"staging-key-456": true,
			"prod-key-789":    true,
			"admin-key-000":   true,
			// Note: disabled-key-999 is intentionally not included (simulating disabled key)
		},
		shouldError: false,
	}
}

// TestNginxAuth tests the nginx auth server behavior using a MockValidator
// This approach isolates the server logic testing from configuration file dependencies
func TestNginxAuth(t *testing.T) {
	validator := getTestValidator()
	authServer := NewServerWithValidator(ServerConfig{}, validator)
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
			validKeys := []string{"dev-key-123", "staging-key-456", "prod-key-789", "admin-key-000"}

			for _, key := range validKeys {
				t.Run(key, func(t *testing.T) {
					req, _ := http.NewRequest("GET", server.URL+"/auth", nil)
					req.Header.Set("apikey", key)

					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatalf("Failed to connect to auth endpoint: %v", err)
					}
					defer resp.Body.Close()

					if resp.StatusCode != http.StatusOK {
						t.Errorf("Expected auth endpoint with valid key %s to return 200, got %d", key, resp.StatusCode)
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
		mockValidator := &MockValidator{
			validKeys: map[string]bool{
				"custom-key-1": true,
				"custom-key-2": true,
			},
			shouldError: false,
		}

		config := ServerConfig{
			LogLevel:       "debug",
			RequestLogging: false,
		}

		authServer := NewServerWithValidator(config, mockValidator)
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
		mockValidator := &MockValidator{
			validKeys:    map[string]bool{},
			shouldError:  true,
			errorMessage: "external service unavailable",
		}

		config := ServerConfig{
			LogLevel:       "debug",
			RequestLogging: false,
		}

		authServer := NewServerWithValidator(config, mockValidator)
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
