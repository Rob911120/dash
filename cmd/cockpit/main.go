package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"dash"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// Clear debug log on startup
	os.Remove("debug.log")

	db, err := dash.ConnectDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cockpit: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Create router from config (falls back to defaults)
	cfg := dash.DefaultRouterConfig()
	router := dash.NewLLMRouter(cfg)

	d, err := dash.New(dash.Config{
		DB:              db,
		FileAllowedRoot: "/",
		Router:          router,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cockpit: dash init: %v\n", err)
		os.Exit(1)
	}

	// Load router config from graph (updates with any stored overrides)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if graphCfg, err := dash.LoadRouterConfig(ctx, d); err == nil {
		router.UpdateConfig(graphCfg)
	}
	d.EnsureProjectDefaults(ctx)
	cancel()

	chatCl := newChatClient(router)

	// Build tool definitions from registry
	defs := d.Registry().All()
	toolDefs := make([]map[string]any, len(defs))
	for i, t := range defs {
		toolDefs[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		}
	}
	chatCl.tools = toolDefs

	sessionID := fmt.Sprintf("cockpit-%d", os.Getpid())
	p := tea.NewProgram(
		newModel(d, chatCl, sessionID, db),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "cockpit: %v\n", err)
		os.Exit(1)
	}
}
