package dash

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	_ "github.com/lib/pq"
)

// EnvOr returns the value of the environment variable key, or fallback if unset/empty.
func EnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ConnectDB builds a PostgreSQL connection string from environment variables,
// opens the connection, and verifies it with a ping.
//
// Environment variable priority:
//  1. DASH_DATABASE_URL (full connection string)
//  2. Individual: DASH_PGHOST/PGHOST, DASH_PGUSER/PGUSER, etc.
func ConnectDB() (*sql.DB, error) {
	dbURL := os.Getenv("DASH_DATABASE_URL")
	if dbURL == "" {
		host := EnvOr("DASH_PGHOST", EnvOr("PGHOST", "localhost"))
		user := EnvOr("DASH_PGUSER", EnvOr("PGUSER", "postgres"))
		dbname := EnvOr("DASH_PGDATABASE", EnvOr("PGDATABASE", "dash"))
		sslmode := EnvOr("DASH_PGSSLMODE", EnvOr("PGSSLMODE", "disable"))
		dbURL = fmt.Sprintf("host=%s user=%s dbname=%s sslmode=%s", host, user, dbname, sslmode)
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}

	return db, nil
}

// LoadEnvFromMCPConfig reads .mcp.json and sets all env vars from mcpServers.*.env.
// Skips vars that are already set in the environment. Returns the number of vars set.
func LoadEnvFromMCPConfig(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	var cfg struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}

	count := 0
	for _, srv := range cfg.MCPServers {
		for k, v := range srv.Env {
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
				count++
			}
		}
	}
	return count, nil
}
