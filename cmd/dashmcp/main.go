// Package main implements the Dash MCP server for Claude Code integration.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"dash"
)

func main() {
	db, err := dash.ConnectDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashmcp: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Create router
	router := dash.NewLLMRouter(dash.DefaultRouterConfig())

	// Create Dash client
	d, err := dash.New(dash.Config{
		DB:              db,
		FileAllowedRoot: dash.EnvOr("DASH_FILE_ROOT", "/"),
		Router:          router,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashmcp: failed to create dash client: %v\n", err)
		os.Exit(1)
	}

	// Load router config from graph
	{
		ctx, cancel := context.WithTimeout(context.Background(), 3e9)
		if graphCfg, err := dash.LoadRouterConfig(ctx, d); err == nil {
			router.UpdateConfig(graphCfg)
		}
		cancel()
	}

	// Create MCP server
	server := dash.NewMCPServer(d)

	// Handle shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Run MCP server
	if err := server.Run(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "dashmcp: server error: %v\n", err)
		os.Exit(1)
	}
}

