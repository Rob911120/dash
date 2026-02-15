package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"dash"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

func main() {
	// Connect to DB
	connStr := fmt.Sprintf("host=%s port=%s dbname=%s user=%s sslmode=disable",
		os.Getenv("PGHOST"), os.Getenv("PGPORT"), os.Getenv("PGDATABASE"), os.Getenv("PGUSER"))
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()

	// Init Dash
	d, err := dash.New(dash.Config{DB: db})
	if err != nil {
		log.Fatalf("dash init: %v", err)
	}

	ctx := context.Background()

	// 1. Create a dummy file to link to
	fileNode, err := d.GetOrCreateNode(ctx, dash.LayerSystem, "file", "/dash/test/component.go", map[string]any{
		"path": "/dash/test/component.go",
	})
	if err != nil {
		log.Fatalf("create file: %v", err)
	}
	fmt.Printf("Created file node: %s\n", fileNode.ID)

	// 2. Create a dummy intent
	intentData, _ := json.Marshal(map[string]any{
		"description": "Improve system stability and testing",
	})
	intentNode := &dash.Node{
		ID:    uuid.New(),
		Layer: dash.LayerContext,
		Type:  "intent",
		Name:  "test-intent",
		Data:  intentData,
	}
	if err := d.CreateNode(ctx, intentNode); err != nil {
		log.Printf("intent creation (might exist): %v", err)
	}

	// 3. Run suggest_improvement tool
	args := map[string]any{
		"title":              "Refactor Test Component",
		"description":        "Split the large component into smaller testable units.",
		"rationale":          "improve system stability",
		"priority":           "high",
		"affected_component": "/dash/test/component.go",
	}

	result := d.RunTool(ctx, "suggest_improvement", args, &dash.ToolOpts{CallerID: "test-script"})

	if !result.Success {
		log.Fatalf("tool failed: %s", result.Error)
	}

	data := result.Data.(map[string]any)
	fmt.Printf("Tool success! Result: %+v\n", data)

	// 4. Verify links in DB
	suggestionID, _ := uuid.Parse(data["id"].(string))

	rows, _ := db.Query(`
		SELECT e.relation, t.name, t.type
		FROM edges e
		JOIN nodes t ON e.target_id = t.id
		WHERE e.source_id = $1`, suggestionID)
	defer rows.Close()

	fmt.Println("\nLinks created:")
	for rows.Next() {
		var rel, name, typ string
		rows.Scan(&rel, &name, &typ)
		fmt.Printf("- %s -> %s (%s)\n", rel, name, typ)
	}
}
