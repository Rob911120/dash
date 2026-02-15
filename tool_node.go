package dash

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

func defNode() *ToolDef {
	return &ToolDef{
		Name:        "node",
		Description: "CRUD operations for graph nodes. Operations: get (by id or layer/type/name), create, update, delete, list.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]any{
				"op":    map[string]any{"type": "string", "enum": []string{"get", "create", "update", "delete", "list"}, "description": "Operation to perform"},
				"id":    map[string]any{"type": "string", "description": "Node UUID (for get/update/delete)"},
				"layer": map[string]any{"type": "string", "enum": []string{"CONTEXT", "SYSTEM", "AUTOMATION"}, "description": "Node layer"},
				"type":  map[string]any{"type": "string", "description": "Node type"},
				"name":  map[string]any{"type": "string", "description": "Node name"},
				"data":  map[string]any{"type": "object", "description": "Node data (for create/update)"},
			},
		},
		Tags: []string{"graph", "write"},
		Fn:   toolNode,
	}
}

func toolNode(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	op, _ := args["op"].(string)
	if op == "" {
		op = "get"
	}

	switch op {
	case "get":
		if idStr, ok := args["id"].(string); ok && idStr != "" {
			id, err := uuid.Parse(idStr)
			if err != nil {
				return nil, fmt.Errorf("invalid UUID: %w", err)
			}
			return d.GetNodeActive(ctx, id)
		}

		layerStr, _ := args["layer"].(string)
		nodeType, _ := args["type"].(string)
		name, _ := args["name"].(string)

		if layerStr != "" && nodeType != "" && name != "" {
			return d.GetNodeByName(ctx, Layer(layerStr), nodeType, name)
		}

		return nil, fmt.Errorf("provide either 'id' or 'layer'+'type'+'name'")

	case "create":
		layerStr, _ := args["layer"].(string)
		nodeType, _ := args["type"].(string)
		name, _ := args["name"].(string)

		if layerStr == "" || nodeType == "" || name == "" {
			return nil, fmt.Errorf("layer, type, and name are required for create")
		}

		node := &Node{
			Layer: Layer(layerStr),
			Type:  nodeType,
			Name:  name,
		}

		if data, ok := args["data"].(map[string]any); ok {
			dataBytes, err := json.Marshal(data)
			if err != nil {
				return nil, fmt.Errorf("invalid data: %w", err)
			}
			node.Data = dataBytes
		}

		if err := d.CreateNode(ctx, node); err != nil {
			return nil, err
		}

		go d.EmbedNode(context.Background(), node)

		// Auto-link tasks to best matching intent
		result := map[string]any{"node": node}
		if nodeType == "task" && layerStr == "CONTEXT" {
			desc := ""
			if data, ok := args["data"].(map[string]any); ok {
				desc, _ = data["description"].(string)
			}
			if intentName, err := d.AutoLinkTaskToIntent(ctx, node.ID, name, desc); err == nil && intentName != "" {
				result["auto_linked_intent"] = intentName
			}
		}
		return result, nil

	case "update":
		idStr, ok := args["id"].(string)
		if !ok || idStr == "" {
			return nil, fmt.Errorf("id is required for update")
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("invalid UUID: %w", err)
		}

		node, err := d.GetNodeActive(ctx, id)
		if err != nil {
			return nil, err
		}

		if layerStr, ok := args["layer"].(string); ok && layerStr != "" {
			node.Layer = Layer(layerStr)
		}
		if nodeType, ok := args["type"].(string); ok && nodeType != "" {
			node.Type = nodeType
		}
		if name, ok := args["name"].(string); ok && name != "" {
			node.Name = name
		}
		if data, ok := args["data"].(map[string]any); ok {
			// Merge new data into existing (never replace)
			var existing map[string]any
			if err := json.Unmarshal(node.Data, &existing); err != nil {
				existing = make(map[string]any)
			}
			for k, v := range data {
				existing[k] = v
			}
			dataBytes, err := json.Marshal(existing)
			if err != nil {
				return nil, fmt.Errorf("invalid data: %w", err)
			}
			node.Data = dataBytes
		}

		if err := d.UpdateNode(ctx, node); err != nil {
			return nil, err
		}
		return node, nil

	case "delete":
		idStr, ok := args["id"].(string)
		if !ok || idStr == "" {
			return nil, fmt.Errorf("id is required for delete")
		}
		id, err := uuid.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf("invalid UUID: %w", err)
		}

		if err := d.SoftDeleteNode(ctx, id); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true, "id": idStr}, nil

	case "list":
		layerStr, _ := args["layer"].(string)
		nodeType, _ := args["type"].(string)

		if layerStr != "" && nodeType != "" {
			return d.ListNodesByLayerType(ctx, Layer(layerStr), nodeType)
		}
		if layerStr != "" {
			return d.ListNodesByLayer(ctx, Layer(layerStr))
		}
		return d.ListNodes(ctx)

	default:
		return nil, fmt.Errorf("unknown operation: %s", op)
	}
}
