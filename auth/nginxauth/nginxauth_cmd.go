package nginxauth

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/interline-io/log"
	"github.com/spf13/pflag"
)

// Command provides CLI interface for nginx auth server
type Command struct {
	// API Key configuration
	APIKeyConfigPath string

	// JWT configuration
	JWTPublicKeyPath string
	JWTAudience      string
	JWTIssuer        string

	// Server configuration
	Bind string
	Port int
	ServerConfig

	// Internal validators
	Validators []Validator
}

func (cmd *Command) AddFlags(fl *pflag.FlagSet) {
	fl.StringVar(&cmd.Bind, "bind", "0.0.0.0", "Bind address")
	fl.IntVar(&cmd.Port, "port", 8080, "Port to listen on")

	// API Key configuration
	fl.StringVar(&cmd.APIKeyConfigPath, "api-key-config", "", "Path to JSON file containing API key configuration")

	// JWT configuration
	fl.StringVar(&cmd.JWTPublicKeyPath, "jwt-public-key", "", "Path to RSA public key file for JWT validation")
	fl.StringVar(&cmd.JWTAudience, "jwt-audience", "", "Expected JWT audience (optional)")
	fl.StringVar(&cmd.JWTIssuer, "jwt-issuer", "", "Expected JWT issuer (optional)")
}

func (cmd *Command) HelpDesc() (string, string) {
	return "Start nginx auth server for API key and JWT validation", `
The nginx auth server provides HTTP authentication for nginx's ngx_http_auth_request_module.
It supports multiple authentication methods:

1. API Keys: Validates keys passed in the "apikey" header
2. JWT Tokens: Validates JWT tokens in the "authorization" header with "Bearer <token>" format

Authentication is attempted in the order validators are configured. The first successful 
validation allows the request.

Server responds with:
- 200 OK if authentication is successful (sets X-Username header)
- 403 Forbidden if authentication fails or no valid credentials provided
- 500 Internal Server Error on validation errors

Configure nginx with:
  auth_request /auth;
  auth_request_set $auth_status $upstream_status;
  auth_request_set $auth_user $upstream_http_x_username;

Examples:
  # API key only
  nginx-auth --api-key-config /path/to/keys.json
  
  # JWT only
  nginx-auth --jwt-public-key /path/to/public.pem --jwt-audience "my-api"
  
  # Both API key and JWT
  nginx-auth --api-key-config /path/to/keys.json --jwt-public-key /path/to/public.pem
`
}

func (cmd *Command) Parse(args []string) error {
	return nil
}

func (cmd *Command) Run(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", cmd.Bind, cmd.Port)

	// Setup validators based on configuration
	if len(cmd.Validators) == 0 {
		validators, err := cmd.setupValidators()
		if err != nil {
			return fmt.Errorf("failed to setup validators: %w", err)
		}
		cmd.Validators = append(cmd.Validators, validators...)
	}

	// Default to empty API key validator
	if len(cmd.Validators) == 0 {
		defaultValidator := NewAPIKeyValidator()
		cmd.Validators = append(cmd.Validators, defaultValidator)
		log.Infof("No validators configured, using empty API key validator")
	}

	// Create the auth server with the configured validators
	authServer := NewServerWithValidators(cmd.ServerConfig, cmd.Validators...)
	mux := authServer.SetupRoutes()
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start server in a goroutine
	go func() {
		log.Infof("nginx auth server starting on %s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Print("shutting down nginx auth server...")

	// Create a context with timeout for graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Errorf("server forced to shutdown: %v", err)
		return err
	}

	log.Print("nginx auth server stopped")
	return nil
}

// setupValidators configures validators based on command line options
func (cmd *Command) setupValidators() ([]Validator, error) {
	var validators []Validator

	// Setup API key validator if config path is provided
	if cmd.APIKeyConfigPath != "" {
		apiKeyValidator := NewAPIKeyValidator()
		if err := apiKeyValidator.LoadConfig(cmd.APIKeyConfigPath); err != nil {
			return nil, fmt.Errorf("failed to load API key config from %s: %w", cmd.APIKeyConfigPath, err)
		}
		validators = append(validators, apiKeyValidator)
		log.Infof("Loaded API key validator from %s", cmd.APIKeyConfigPath)
	}

	// Setup JWT validator if public key path is provided
	if cmd.JWTPublicKeyPath != "" {
		jwtConfig := JWTConfig{
			PublicKeyPath: cmd.JWTPublicKeyPath,
			Audience:      cmd.JWTAudience,
			Issuer:        cmd.JWTIssuer,
		}

		jwtValidator, err := NewJWTValidator(jwtConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWT validator: %w", err)
		}

		validators = append(validators, jwtValidator)
		log.Infof("Loaded JWT validator with public key from %s", cmd.JWTPublicKeyPath)
		if cmd.JWTAudience != "" {
			log.Infof("JWT audience validation enabled: %s", cmd.JWTAudience)
		}
		if cmd.JWTIssuer != "" {
			log.Infof("JWT issuer validation enabled: %s", cmd.JWTIssuer)
		}
	}

	return validators, nil
}
