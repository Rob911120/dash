// Package main implements the dashhook CLI for processing Claude Code hook events.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"dash"
)

func main() {
	// Read input from stdin
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashhook: failed to read stdin: %v\n", err)
		os.Exit(1)
	}

	// Skip empty input
	if len(input) == 0 {
		os.Exit(0)
	}

	db, err := dash.ConnectDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashhook: %v\n", err)
		// Exit 0 to not block Claude
		os.Exit(0)
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
		fmt.Fprintf(os.Stderr, "dashhook: failed to create dash client: %v\n", err)
		os.Exit(0)
	}

	// Process hook event
	ctx := context.Background()
	output, err := d.ProcessHookEvent(ctx, input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashhook: hook processing failed: %v\n", err)
		// Exit 0 to not block Claude - errors are logged but don't stop execution
	}

	// Handle output based on type
	if output != nil {
		if output.IsJSON || output.SystemMessage != "" {
			// PreToolUse: use hookSpecificOutput format for Claude Code
			// permissionDecision: "allow" lets the tool run, but shows the reason
			jsonOutput := map[string]any{
				"hookSpecificOutput": map[string]any{
					"hookEventName":            "PreToolUse",
					"permissionDecision":       "allow",
					"permissionDecisionReason": output.SystemMessage,
				},
			}
			jsonBytes, _ := json.Marshal(jsonOutput)
			fmt.Print(string(jsonBytes))
		} else if output.Content != "" {
			// SessionStart uses plain text
			fmt.Print(output.Content)
		}
	}

	// Exit 0 = allow Claude to continue
	os.Exit(0)
}

