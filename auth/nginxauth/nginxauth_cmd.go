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
	APIKeyConfigPath string
	Validator        APIKeyValidator
	Bind             string
	Port             int
	ServerConfig
}

func (cmd *Command) AddFlags(fl *pflag.FlagSet) {
	fl.StringVar(&cmd.Bind, "bind", "0.0.0.0", "Bind address")
	fl.IntVar(&cmd.Port, "port", 8080, "Port to listen on")
}

func (cmd *Command) HelpDesc() (string, string) {
	return "Start nginx auth server for API key validation", `
The nginx auth server provides HTTP authentication for nginx's ngx_http_auth_request_module.
It validates API keys passed in the "apikey" header and responds with:
- 200 OK if the API key is valid
- 403 Forbidden if the API key is invalid or missing

Configure nginx with:
  auth_request /auth;
  auth_request_set $auth_status $upstream_status;
`
}

func (cmd *Command) Parse(args []string) error {
	return nil
}

func (cmd *Command) Run(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", cmd.Bind, cmd.Port)

	// Setup validator
	if cmd.APIKeyConfigPath != "" {
		configValidator := NewConfigBasedValidator()
		if err := configValidator.LoadConfig(cmd.APIKeyConfigPath); err != nil {
			return fmt.Errorf("failed to load API key config: %w", err)
		}
		cmd.Validator = configValidator
		log.Infof("Loaded API keys from %s", cmd.APIKeyConfigPath)
	}
	if cmd.Validator == nil {
		cmd.Validator = NewConfigBasedValidator()
	}

	// Create the auth server with the configured validator
	authServer := NewServerWithValidator(cmd.ServerConfig, cmd.Validator)
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
