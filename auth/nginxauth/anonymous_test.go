package nginxauth

import (
	"net/http"
	"testing"
)

func TestDefaultValidator_Validate_NoUsername(t *testing.T) {
	validator := NewDefaultValidator()
	req, _ := http.NewRequest("GET", "/test", nil)

	username, valid, err := validator.Validate(req)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !valid {
		t.Errorf("Expected valid=true, got %v", valid)
	}
	if username != "" {
		t.Errorf("Expected empty username, got %q", username)
	}
}

func TestDefaultValidator_Validate_WithUsername(t *testing.T) {
	expectedUsername := "anonymous"
	validator := NewDefaultValidatorWithUsername(expectedUsername)
	req, _ := http.NewRequest("GET", "/test", nil)

	username, valid, err := validator.Validate(req)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !valid {
		t.Errorf("Expected valid=true, got %v", valid)
	}
	if username != expectedUsername {
		t.Errorf("Expected username %q, got %q", expectedUsername, username)
	}
}

func TestDefaultValidator_SetDefaultUsername(t *testing.T) {
	validator := NewDefaultValidator()
	req, _ := http.NewRequest("GET", "/test", nil)

	// Initially no username
	username, valid, err := validator.Validate(req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !valid {
		t.Errorf("Expected valid=true, got %v", valid)
	}
	if username != "" {
		t.Errorf("Expected empty username, got %q", username)
	}

	// Set username and test again
	expectedUsername := "guest"
	validator.SetDefaultUsername(expectedUsername)
	username, valid, err = validator.Validate(req)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !valid {
		t.Errorf("Expected valid=true, got %v", valid)
	}
	if username != expectedUsername {
		t.Errorf("Expected username %q, got %q", expectedUsername, username)
	}
}

func TestDefaultValidator_AlwaysSucceeds(t *testing.T) {
	validator := NewDefaultValidatorWithUsername("test")

	// Test with various request configurations to ensure it always succeeds
	testCases := []struct {
		name string
		req  *http.Request
	}{
		{
			name: "GET request",
			req:  mustNewRequest("GET", "/test", nil),
		},
		{
			name: "POST request",
			req:  mustNewRequest("POST", "/test", nil),
		},
		{
			name: "Request with headers",
			req: func() *http.Request {
				req := mustNewRequest("GET", "/test", nil)
				req.Header.Set("Authorization", "Bearer invalid-token")
				req.Header.Set("apikey", "invalid-key")
				return req
			}(),
		},
		{
			name: "Request with query params",
			req:  mustNewRequest("GET", "/test?param=value", nil),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			username, valid, err := validator.Validate(tc.req)
			if err != nil {
				t.Errorf("Expected no error, got %v", err)
			}
			if !valid {
				t.Errorf("Expected valid=true, got %v", valid)
			}
			if username != "test" {
				t.Errorf("Expected username %q, got %q", "test", username)
			}
		})
	}
}

func mustNewRequest(method, url string, body interface{}) *http.Request {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		panic(err)
	}
	return req
}
