package nginxauth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/form3tech-oss/jwt-go"
	"github.com/interline-io/log"
)

// JWTConfig represents JWT validation configuration
type JWTConfig struct {
	PublicKeyPath string `json:"publicKeyPath"`
	Audience      string `json:"audience"`
	Issuer        string `json:"issuer"`
}

// JWTValidator implements the Validator interface for JWT authentication.
// It checks for JWT tokens in the "authorization" header with "Bearer <token>" format.
type JWTValidator struct {
	publicKey *rsa.PublicKey
	audience  string
	issuer    string
}

// NewJWTValidator creates a new JWT validator
func NewJWTValidator(config JWTConfig) (*JWTValidator, error) {
	publicKey, err := loadRSAPublicKey(config.PublicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load RSA public key: %w", err)
	}

	return &JWTValidator{
		publicKey: publicKey,
		audience:  config.Audience,
		issuer:    config.Issuer,
	}, nil
}

// Validate implements the Validator interface by checking for JWT tokens in the authorization header
func (v *JWTValidator) Validate(r *http.Request) (string, bool, error) {
	authHeader := r.Header.Get("authorization")
	if authHeader == "" {
		return "", false, nil // No authorization header, let other validators try
	}

	// Extract token from "Bearer <token>" format
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == authHeader {
		// No "Bearer " prefix found
		log.Debugf("Invalid authorization header format from %s", r.RemoteAddr)
		return "", false, nil
	}

	return v.validateJWT(token)
}

// validateJWT validates a JWT token and returns the username/subject.
// It validates the RSA signature, expiration, audience, and issuer claims.
// The username is extracted from "sub", "username", or "preferred_username" claims.
func (v *JWTValidator) validateJWT(tokenString string) (string, bool, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return v.publicKey, nil
	})

	if err != nil {
		log.Debugf("JWT parsing error: %v", err)
		return "", false, nil
	}

	if !token.Valid {
		log.Debugf("Invalid JWT token")
		return "", false, nil
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		log.Debugf("Invalid JWT claims format")
		return "", false, nil
	}

	// Validate audience if specified
	if v.audience != "" {
		if aud, ok := claims["aud"].(string); !ok || aud != v.audience {
			log.Debugf("JWT audience validation failed: expected %s, got %v", v.audience, claims["aud"])
			return "", false, nil
		}
	}

	// Validate issuer if specified
	if v.issuer != "" {
		if iss, ok := claims["iss"].(string); !ok || iss != v.issuer {
			log.Debugf("JWT issuer validation failed: expected %s, got %v", v.issuer, claims["iss"])
			return "", false, nil
		}
	}

	// Validate expiration
	if exp, ok := claims["exp"].(float64); ok {
		if time.Now().Unix() > int64(exp) {
			log.Debugf("JWT token expired")
			return "", false, nil
		}
	}

	// Extract username/subject
	username := ""
	if sub, ok := claims["sub"].(string); ok {
		username = sub
	} else if name, ok := claims["username"].(string); ok {
		username = name
	} else if name, ok := claims["preferred_username"].(string); ok {
		username = name
	}

	if username == "" {
		log.Debugf("No username found in JWT claims")
		return "", false, nil
	}

	return username, true, nil
}

// loadRSAPublicKey loads an RSA public key from a PEM file
func loadRSAPublicKey(path string) (*rsa.PublicKey, error) {
	keyData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read public key file: %w", err)
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}

	var publicKey *rsa.PublicKey
	switch block.Type {
	case "PUBLIC KEY":
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse PKIX public key: %w", err)
		}
		var ok bool
		publicKey, ok = pub.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("public key is not RSA")
		}
	case "RSA PUBLIC KEY":
		pub, err := x509.ParsePKCS1PublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse PKCS1 public key: %w", err)
		}
		publicKey = pub
	default:
		return nil, fmt.Errorf("unsupported key type: %s", block.Type)
	}

	return publicKey, nil
}
