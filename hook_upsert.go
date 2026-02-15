package dash

import (
	"context"
	"encoding/json"
)

// GetOrCreateNode retrieves an existing node by layer/type/name or creates a new one.
// This handles race conditions by catching unique constraint violations.
func (d *Dash) GetOrCreateNode(ctx context.Context, layer Layer, nodeType, name string, data map[string]any) (*Node, error) {
	// Try to get existing node first
	node, err := d.GetNodeByName(ctx, layer, nodeType, name)
	if err == nil {
		return node, nil
	}
	if err != ErrNodeNotFound {
		return nil, err
	}

	// Node doesn't exist, create it
	var dataJSON json.RawMessage
	if data != nil {
		dataJSON, err = json.Marshal(data)
		if err != nil {
			return nil, err
		}
	} else {
		dataJSON = json.RawMessage(`{}`)
	}

	node = &Node{
		Layer: layer,
		Type:  nodeType,
		Name:  name,
		Data:  dataJSON,
	}

	if err := d.CreateNode(ctx, node); err != nil {
		// Handle race condition: another process may have created it
		// Try to get the node again
		existingNode, getErr := d.GetNodeByName(ctx, layer, nodeType, name)
		if getErr == nil {
			return existingNode, nil
		}
		// Return the original create error
		return nil, err
	}

	return node, nil
}

// UpdateNodeData updates the data field of an existing node by merging new data.
func (d *Dash) UpdateNodeData(ctx context.Context, node *Node, updates map[string]any) error {
	var existing map[string]any
	if err := json.Unmarshal(node.Data, &existing); err != nil {
		existing = make(map[string]any)
	}

	// Merge updates into existing
	for k, v := range updates {
		existing[k] = v
	}

	dataJSON, err := json.Marshal(existing)
	if err != nil {
		return err
	}

	node.Data = dataJSON
	return d.UpdateNode(ctx, node)
}
