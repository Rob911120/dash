package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"dash"

	_ "github.com/lib/pq"
)

func main() {
	// Connect to DB
	dbConnStr := os.Getenv("DASH_DB")
	if dbConnStr == "" {
		dbConnStr = "host=localhost user=dash password=dash dbname=dash sslmode=disable"
	}
	db, err := sql.Open("postgres", dbConnStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Init dash
	d, err := dash.New(dash.Config{DB: db, FileAllowedRoot: "/"})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	fmt.Println("=== FORGET TOOL TEST ===")

	// Test 1: Dry run - search for "Forget" (case insensitive)
	fmt.Println("Test 1: Dry run search for 'Forget' (should match the insight we just created)")
	result1 := d.RunTool(ctx, "forget", map[string]any{
		"query":   "Forget",
		"dry_run": true,
	}, nil)
	printResult(result1)

	// Test 2: Dry run with type filter - search decisions containing "Agent"
	fmt.Println("\nTest 2: Dry run search for 'decision' type nodes containing 'Agent'")
	result2 := d.RunTool(ctx, "forget", map[string]any{
		"query":   "Agent",
		"type":    "decision",
		"dry_run": true,
	}, nil)
	printResult(result2)

	// Test 3: Search that finds nothing
	fmt.Println("\nTest 3: Search for non-existent term 'xyz123nonexistent'")
	result3 := d.RunTool(ctx, "forget", map[string]any{
		"query":   "xyz123nonexistent",
		"dry_run": true,
	}, nil)
	printResult(result3)

	// Test 4: Show challenge behavior (dry_run=false without explicit IDs)
	fmt.Println("\nTest 4: Live delete without confirm (should trigger challenge)")
	result4 := d.RunTool(ctx, "forget", map[string]any{
		"query":   "xyz123nonexistent",
		"dry_run": false,
	}, nil)
	printResult(result4)

	// Test 5: First create a test node, then delete it with explicit ID
	fmt.Println("\nTest 5: Create test node, then delete with confirm_ids")
	testResult := d.RunTool(ctx, "remember", map[string]any{
		"type": "todo",
		"text": "Test-forget-demo: this is a temporary test node for forget tool",
	}, nil)
	printResult(testResult)
	
	// Extract the ID from the result
	var testID string
	if testResult.Success && testResult.Data != nil {
		if dataMap, ok := testResult.Data.(map[string]any); ok {
			if id, ok := dataMap["id"].(string); ok {
				testID = id
				fmt.Printf("\nCreated test node with ID: %s\n", testID)
				
				// Now delete it with confirm_ids (should skip challenge)
				fmt.Println("\nDeleting with explicit confirm_ids (no challenge expected):")
				deleteResult := d.RunTool(ctx, "forget", map[string]any{
					"query":       "test-forget-demo",
					"confirm_ids": []any{testID},
				}, nil)
				printResult(deleteResult)
			}
		}
	}

	fmt.Println("\n=== TESTS COMPLETE ===")
	fmt.Println("\nTo actually delete nodes, use either:")
	fmt.Println("  1. dry_run=true first, then confirm_ids with specific IDs")
	fmt.Println("  2. Or the TUI/MCP layer handles confirm=true after challenge")
}

func printResult(result *dash.ToolResult) {
	data, _ := json.MarshalIndent(result.Data, "", "  ")
	fmt.Printf("Success: %v\n", result.Success)
	if result.Error != "" {
		fmt.Printf("Error: %s\n", result.Error)
	}
	if result.Challenge != nil {
		fmt.Printf("Challenge: %s\n", result.Challenge.Question)
	}
	fmt.Printf("Data: %s\n", string(data))
}
