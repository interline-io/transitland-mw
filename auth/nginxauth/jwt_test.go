package nginxauth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/form3tech-oss/jwt-go"
)

func generateTestKeyPair(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate RSA key pair: %v", err)
	}
	return privateKey, &privateKey.PublicKey
}

func createTestKeyFiles(t *testing.T) (string, *rsa.PrivateKey) {
	// Generate test key pair
	privateKey, publicKey := generateTestKeyPair(t)

	// Create temporary directory
	tempDir := t.TempDir()

	// Create public key PEM
	publicKeyDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatalf("Failed to marshal public key: %v", err)
	}

	publicKeyPEM := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyDER,
	}

	// Write public key file
	publicKeyPath := filepath.Join(tempDir, "public_key.pem")
	publicKeyFile, err := os.Create(publicKeyPath)
	if err != nil {
		t.Fatalf("Failed to create public key file: %v", err)
	}
	defer publicKeyFile.Close()

	err = pem.Encode(publicKeyFile, publicKeyPEM)
	if err != nil {
		t.Fatalf("Failed to write public key PEM: %v", err)
	}

	return publicKeyPath, privateKey
}

func generateValidJWT(t *testing.T, privateKey *rsa.PrivateKey, claims jwt.MapClaims) string {
	// Create token
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("Failed to sign token: %v", err)
	}

	return tokenString
}

func TestJWTValidator_Validate_NoAuthHeader(t *testing.T) {
	publicKeyPath, _ := createTestKeyFiles(t)

	config := JWTConfig{
		PublicKeyPath: publicKeyPath,
	}
	validator, err := NewJWTValidator(config)
	if err != nil {
		t.Fatalf("Failed to create JWT validator: %v", err)
	}

	req, _ := http.NewRequest("GET", "/test", nil)

	username, valid, err := validator.Validate(req)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if valid {
		t.Errorf("Expected valid=false, got %v", valid)
	}
	if username != "" {
		t.Errorf("Expected empty username, got %q", username)
	}
}

func TestJWTValidator_Validate_ValidJWT(t *testing.T) {
	publicKeyPath, privateKey := createTestKeyFiles(t)

	config := JWTConfig{
		PublicKeyPath: publicKeyPath,
	}
	validator, err := NewJWTValidator(config)
	if err != nil {
		t.Fatalf("Failed to create JWT validator: %v", err)
	}

	// Generate valid JWT
	claims := jwt.MapClaims{
		"sub": "testuser",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	tokenString := generateValidJWT(t, privateKey, claims)

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)

	username, valid, err := validator.Validate(req)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if !valid {
		t.Errorf("Expected valid=true, got %v", valid)
	}
	if username != "testuser" {
		t.Errorf("Expected username 'testuser', got %q", username)
	}
}

func TestJWTValidator_Validate_InvalidAuthHeaderFormat(t *testing.T) {
	publicKeyPath, _ := createTestKeyFiles(t)

	config := JWTConfig{
		PublicKeyPath: publicKeyPath,
	}
	validator, err := NewJWTValidator(config)
	if err != nil {
		t.Fatalf("Failed to create JWT validator: %v", err)
	}

	testCases := []struct {
		name       string
		authHeader string
	}{
		{"No Bearer prefix", "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9..."},
		{"Wrong prefix", "Basic dXNlcjpwYXNz"},
		{"Just Bearer", "Bearer"},
		{"Bearer with space only", "Bearer "},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/test", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			username, valid, err := validator.Validate(req)

			if err != nil {
				t.Errorf("Expected no error, got %v", err)
			}
			if valid {
				t.Errorf("Expected valid=false, got %v", valid)
			}
			if username != "" {
				t.Errorf("Expected empty username, got %q", username)
			}
		})
	}
}

func TestJWTValidator_Validate_MalformedJWT(t *testing.T) {
	publicKeyPath, _ := createTestKeyFiles(t)

	config := JWTConfig{
		PublicKeyPath: publicKeyPath,
	}
	validator, err := NewJWTValidator(config)
	if err != nil {
		t.Fatalf("Failed to create JWT validator: %v", err)
	}

	testCases := []struct {
		name  string
		token string
	}{
		{"Invalid base64", "Bearer invalid.token.here"},
		{"Missing parts", "Bearer eyJhbGciOiJSUzI1NiJ9"},
		{"Empty token", "Bearer "},
		{"Random string", "Bearer not-a-jwt-token"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/test", nil)
			req.Header.Set("Authorization", tc.token)

			username, valid, err := validator.Validate(req)

			if err != nil {
				t.Errorf("Expected no error, got %v", err)
			}
			if valid {
				t.Errorf("Expected valid=false, got %v", valid)
			}
			if username != "" {
				t.Errorf("Expected empty username, got %q", username)
			}
		})
	}
}

func TestJWTValidator_Validate_ExpiredJWT(t *testing.T) {
	publicKeyPath, privateKey := createTestKeyFiles(t)

	config := JWTConfig{
		PublicKeyPath: publicKeyPath,
	}
	validator, err := NewJWTValidator(config)
	if err != nil {
		t.Fatalf("Failed to create JWT validator: %v", err)
	}

	// Generate expired JWT
	claims := jwt.MapClaims{
		"sub": "testuser",
		"exp": time.Now().Add(-time.Hour).Unix(), // Expired 1 hour ago
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
	}
	tokenString := generateValidJWT(t, privateKey, claims)

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)

	username, valid, err := validator.Validate(req)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if valid {
		t.Errorf("Expected valid=false, got %v", valid)
	}
	if username != "" {
		t.Errorf("Expected empty username, got %q", username)
	}
}

func TestNewJWTValidator_InvalidPublicKey(t *testing.T) {
	// Create temporary file with invalid key
	tempDir := t.TempDir()
	invalidKeyPath := filepath.Join(tempDir, "invalid_key.pem")
	err := os.WriteFile(invalidKeyPath, []byte("invalid key content"), 0644)
	if err != nil {
		t.Fatalf("Failed to write invalid key file: %v", err)
	}

	config := JWTConfig{
		PublicKeyPath: invalidKeyPath,
	}

	_, err = NewJWTValidator(config)
	if err == nil {
		t.Errorf("Expected error for invalid public key, got nil")
	}
}

func TestNewJWTValidator_MissingKeyFile(t *testing.T) {
	config := JWTConfig{
		PublicKeyPath: "/nonexistent/path/key.pem",
	}

	_, err := NewJWTValidator(config)
	if err == nil {
		t.Errorf("Expected error for missing key file, got nil")
	}
}
