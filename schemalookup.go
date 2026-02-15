package dash

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	queryGetSchema = `
		SELECT data
		FROM nodes
		WHERE layer = 'AUTOMATION'
		  AND type = 'schema'
		  AND data->>'for_layer' = $1
		  AND data->>'for_type' = $2
		  AND deleted_at IS NULL
		LIMIT 1`

	queryListSchemas = `
		SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
		FROM nodes
		WHERE layer = 'AUTOMATION'
		  AND type = 'schema'
		  AND deleted_at IS NULL
		ORDER BY name`

	queryGetSchemaByName = `
		SELECT data
		FROM nodes
		WHERE layer = 'AUTOMATION'
		  AND type = 'schema'
		  AND name = $1
		  AND deleted_at IS NULL`
)

// GetSchema retrieves the schema for a given layer and type.
func (d *Dash) GetSchema(ctx context.Context, layer Layer, nodeType string) (map[string]any, error) {
	var dataBytes []byte
	err := d.db.QueryRowContext(ctx, queryGetSchema, string(layer), nodeType).Scan(&dataBytes)
	if err != nil {
		return nil, ErrNodeNotFound
	}

	var schema map[string]any
	if err := json.Unmarshal(dataBytes, &schema); err != nil {
		return nil, fmt.Errorf("invalid schema data: %w", err)
	}

	return schema, nil
}

// GetSchemaByName retrieves a schema by its name.
func (d *Dash) GetSchemaByName(ctx context.Context, name string) (map[string]any, error) {
	var dataBytes []byte
	err := d.db.QueryRowContext(ctx, queryGetSchemaByName, name).Scan(&dataBytes)
	if err != nil {
		return nil, ErrNodeNotFound
	}

	var schema map[string]any
	if err := json.Unmarshal(dataBytes, &schema); err != nil {
		return nil, fmt.Errorf("invalid schema data: %w", err)
	}

	return schema, nil
}

// ListSchemas retrieves all schema definitions.
func (d *Dash) ListSchemas(ctx context.Context) ([]*Node, error) {
	rows, err := d.db.QueryContext(ctx, queryListSchemas)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanNodes(rows)
}

// SchemaInfo provides parsed information about a schema.
type SchemaInfo struct {
	Name        string                 `json:"name"`
	ForLayer    Layer                  `json:"for_layer"`
	ForType     string                 `json:"for_type"`
	Description string                 `json:"description"`
	Fields      map[string]*FieldInfo  `json:"fields"`
}

// FieldInfo describes a field in a schema.
type FieldInfo struct {
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Default     any      `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Values      []string `json:"values,omitempty"` // For enum types
	Min         *float64 `json:"min,omitempty"`    // For numeric types
	Max         *float64 `json:"max,omitempty"`    // For numeric types
}

// GetSchemaInfo retrieves and parses schema info for a layer/type.
func (d *Dash) GetSchemaInfo(ctx context.Context, layer Layer, nodeType string) (*SchemaInfo, error) {
	schema, err := d.GetSchema(ctx, layer, nodeType)
	if err != nil {
		return nil, err
	}

	info := &SchemaInfo{
		ForLayer: layer,
		ForType:  nodeType,
		Fields:   make(map[string]*FieldInfo),
	}

	if desc, ok := schema["description"].(string); ok {
		info.Description = desc
	}

	// Parse fields
	if fields, ok := schema["fields"].(map[string]any); ok {
		for fieldName, fieldDef := range fields {
			fd, ok := fieldDef.(map[string]any)
			if !ok {
				continue
			}

			fieldInfo := &FieldInfo{}

			if t, ok := fd["type"].(string); ok {
				fieldInfo.Type = t
			}
			if r, ok := fd["required"].(bool); ok {
				fieldInfo.Required = r
			}
			if d, ok := fd["default"]; ok {
				fieldInfo.Default = d
			}
			if desc, ok := fd["description"].(string); ok {
				fieldInfo.Description = desc
			}

			// Enum values
			if values, ok := fd["values"].([]any); ok {
				for _, v := range values {
					if s, ok := v.(string); ok {
						fieldInfo.Values = append(fieldInfo.Values, s)
					}
				}
			}

			// Numeric constraints
			if min, ok := fd["min"].(float64); ok {
				fieldInfo.Min = &min
			}
			if max, ok := fd["max"].(float64); ok {
				fieldInfo.Max = &max
			}

			info.Fields[fieldName] = fieldInfo
		}
	}

	return info, nil
}

// HasSchema checks if a schema exists for a given layer and type.
func (d *Dash) HasSchema(ctx context.Context, layer Layer, nodeType string) (bool, error) {
	_, err := d.GetSchema(ctx, layer, nodeType)
	if err == ErrNodeNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CreateSchema creates a new schema definition.
func (d *Dash) CreateSchema(ctx context.Context, name string, forLayer Layer, forType string, fields map[string]*FieldInfo, description string) (*Node, error) {
	// Build schema data
	fieldsMap := make(map[string]any)
	for fieldName, fieldInfo := range fields {
		fieldDef := map[string]any{
			"type":     fieldInfo.Type,
			"required": fieldInfo.Required,
		}
		if fieldInfo.Default != nil {
			fieldDef["default"] = fieldInfo.Default
		}
		if fieldInfo.Description != "" {
			fieldDef["description"] = fieldInfo.Description
		}
		if len(fieldInfo.Values) > 0 {
			fieldDef["values"] = fieldInfo.Values
		}
		if fieldInfo.Min != nil {
			fieldDef["min"] = *fieldInfo.Min
		}
		if fieldInfo.Max != nil {
			fieldDef["max"] = *fieldInfo.Max
		}
		fieldsMap[fieldName] = fieldDef
	}

	schemaData := map[string]any{
		"for_layer":   string(forLayer),
		"for_type":    forType,
		"description": description,
		"fields":      fieldsMap,
	}

	dataBytes, err := json.Marshal(schemaData)
	if err != nil {
		return nil, err
	}

	node := &Node{
		Layer: LayerAutomation,
		Type:  "schema",
		Name:  name,
		Data:  dataBytes,
	}

	if err := d.CreateNode(ctx, node); err != nil {
		return nil, err
	}

	return node, nil
}
