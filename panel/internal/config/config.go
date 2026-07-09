// Package config holds panel process configuration (CLI flags).
package config

// Config is the runtime configuration for the panel process.
type Config struct {
	// DBPath is the SQLite database path.
	DBPath string
	// ListenAddr is the HTTP listen address (e.g. ":8080").
	// Empty means use the value stored in settings (default ":8080").
	ListenAddr string
	// SessionSecret signs JWT session cookies. Empty means generate a random secret at startup.
	SessionSecret string
}
