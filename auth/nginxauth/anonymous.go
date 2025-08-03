package nginxauth

import "net/http"

// DefaultValidator implements the Validator interface as a default backstop validator.
// It always returns valid=true with an optional default username.
// This validator should typically be placed last in the validator chain to ensure
// that all requests are allowed if no other validators succeed.
type DefaultValidator struct {
	defaultUsername string
}

// NewDefaultValidator creates a new default validator
func NewDefaultValidator() *DefaultValidator {
	return &DefaultValidator{
		defaultUsername: "",
	}
}

// NewDefaultValidatorWithUsername creates a new default validator with a default username
func NewDefaultValidatorWithUsername(username string) *DefaultValidator {
	return &DefaultValidator{
		defaultUsername: username,
	}
}

// Validate implements the Validator interface by always returning success.
// Returns the configured default username (or empty string if none configured).
func (v *DefaultValidator) Validate(r *http.Request) (string, bool, error) {
	return v.defaultUsername, true, nil
}

// SetDefaultUsername updates the default username for this validator
func (v *DefaultValidator) SetDefaultUsername(username string) {
	v.defaultUsername = username
}
