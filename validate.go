package dash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

var (
	// ErrValidationFailed is returned when validation fails.
	ErrValidationFailed = errors.New("validation failed")

	// ErrMissingRequired is returned when a required field is missing.
	ErrMissingRequired = errors.New("missing required field")

	// ErrInvalidType is returned when a field has an invalid type.
	ErrInvalidType = errors.New("invalid type")

	// ErrInvalidValue is returned when a field has an invalid value.
	ErrInvalidValue = errors.New("invalid value")
)

// ValidationError holds details about a validation failure.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Value   any    `json:"value,omitempty"`
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationErrors is a collection of validation errors.
type ValidationErrors []*ValidationError

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return "no errors"
	}
	if len(e) == 1 {
		return e[0].Error()
	}
	return fmt.Sprintf("%d validation errors: %s (and %d more)", len(e), e[0].Error(), len(e)-1)
}

// ValidateArgs validates arguments against a JSON schema.
func ValidateArgs(args map[string]any, schema map[string]any) error {
	var errs ValidationErrors

	// Check required fields
	if required, ok := schema["required"].([]any); ok {
		for _, r := range required {
			fieldName, ok := r.(string)
			if !ok {
				continue
			}
			if _, exists := args[fieldName]; !exists {
				errs = append(errs, &ValidationError{
					Field:   fieldName,
					Message: "required field is missing",
				})
			}
		}
	}

	// Validate properties
	if properties, ok := schema["properties"].(map[string]any); ok {
		for fieldName, fieldSchema := range properties {
			value, exists := args[fieldName]
			if !exists {
				continue // Not present, checked by required above
			}

			fs, ok := fieldSchema.(map[string]any)
			if !ok {
				continue
			}

			if err := validateField(fieldName, value, fs); err != nil {
				if ve, ok := err.(*ValidationError); ok {
					errs = append(errs, ve)
				} else {
					errs = append(errs, &ValidationError{Field: fieldName, Message: err.Error()})
				}
			}
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func validateField(name string, value any, schema map[string]any) error {
	// Get expected type
	expectedType, _ := schema["type"].(string)

	switch expectedType {
	case "string":
		str, ok := value.(string)
		if !ok {
			return &ValidationError{Field: name, Message: "expected string", Value: value}
		}
		// Check minLength
		if minLen, ok := schema["minLength"].(float64); ok {
			if len(str) < int(minLen) {
				return &ValidationError{
					Field:   name,
					Message: fmt.Sprintf("string length must be at least %d", int(minLen)),
					Value:   str,
				}
			}
		}
		// Check maxLength
		if maxLen, ok := schema["maxLength"].(float64); ok {
			if len(str) > int(maxLen) {
				return &ValidationError{
					Field:   name,
					Message: fmt.Sprintf("string length must be at most %d", int(maxLen)),
					Value:   str,
				}
			}
		}
		// Check enum
		if enum, ok := schema["enum"].([]any); ok {
			valid := false
			for _, e := range enum {
				if e == str {
					valid = true
					break
				}
			}
			if !valid {
				return &ValidationError{
					Field:   name,
					Message: fmt.Sprintf("value must be one of: %v", enum),
					Value:   str,
				}
			}
		}

	case "integer":
		var num float64
		switch v := value.(type) {
		case float64:
			num = v
		case int:
			num = float64(v)
		case int64:
			num = float64(v)
		default:
			return &ValidationError{Field: name, Message: "expected integer", Value: value}
		}
		// Check minimum
		if min, ok := schema["minimum"].(float64); ok {
			if num < min {
				return &ValidationError{
					Field:   name,
					Message: fmt.Sprintf("value must be at least %v", min),
					Value:   num,
				}
			}
		}
		// Check maximum
		if max, ok := schema["maximum"].(float64); ok {
			if num > max {
				return &ValidationError{
					Field:   name,
					Message: fmt.Sprintf("value must be at most %v", max),
					Value:   num,
				}
			}
		}

	case "number":
		var num float64
		switch v := value.(type) {
		case float64:
			num = v
		case int:
			num = float64(v)
		case int64:
			num = float64(v)
		default:
			return &ValidationError{Field: name, Message: "expected number", Value: value}
		}
		if min, ok := schema["minimum"].(float64); ok {
			if num < min {
				return &ValidationError{
					Field:   name,
					Message: fmt.Sprintf("value must be at least %v", min),
					Value:   num,
				}
			}
		}

	case "boolean":
		if _, ok := value.(bool); !ok {
			return &ValidationError{Field: name, Message: "expected boolean", Value: value}
		}

	case "array":
		arr, ok := value.([]any)
		if !ok {
			// Try other array types
			val := reflect.ValueOf(value)
			if val.Kind() != reflect.Slice && val.Kind() != reflect.Array {
				return &ValidationError{Field: name, Message: "expected array", Value: value}
			}
			// Convert to []any for further validation
			arr = make([]any, val.Len())
			for i := 0; i < val.Len(); i++ {
				arr[i] = val.Index(i).Interface()
			}
		}
		// Validate items if schema provided
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for i, item := range arr {
				if err := validateField(fmt.Sprintf("%s[%d]", name, i), item, itemSchema); err != nil {
					return err
				}
			}
		}

	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return &ValidationError{Field: name, Message: "expected object", Value: value}
		}
		// Validate properties if schema provided
		if properties, ok := schema["properties"].(map[string]any); ok {
			for propName, propSchema := range properties {
				if propValue, exists := obj[propName]; exists {
					if ps, ok := propSchema.(map[string]any); ok {
						if err := validateField(fmt.Sprintf("%s.%s", name, propName), propValue, ps); err != nil {
							return err
						}
					}
				}
			}
		}
	}

	return nil
}

// ValidateNode validates a node against its schema (if one exists).
func (d *Dash) ValidateNode(ctx context.Context, node *Node) error {
	// Look up schema for this layer/type
	schema, err := d.GetSchema(ctx, node.Layer, node.Type)
	if err != nil {
		if err == ErrNodeNotFound {
			// No schema defined, validation passes
			return nil
		}
		return err
	}

	// Parse node data
	var nodeData map[string]any
	if err := json.Unmarshal(node.Data, &nodeData); err != nil {
		return fmt.Errorf("invalid node data: %w", err)
	}

	// Get fields schema
	fields, ok := schema["fields"].(map[string]any)
	if !ok {
		return nil // No fields defined
	}

	var errs ValidationErrors

	// Check each field
	for fieldName, fieldSchema := range fields {
		fs, ok := fieldSchema.(map[string]any)
		if !ok {
			continue
		}

		value, exists := nodeData[fieldName]

		// Check required
		if required, ok := fs["required"].(bool); ok && required && !exists {
			errs = append(errs, &ValidationError{
				Field:   fieldName,
				Message: "required field is missing",
			})
			continue
		}

		if !exists {
			continue
		}

		// Validate the field
		if err := validateSchemaField(fieldName, value, fs); err != nil {
			if ve, ok := err.(*ValidationError); ok {
				errs = append(errs, ve)
			} else {
				errs = append(errs, &ValidationError{Field: fieldName, Message: err.Error()})
			}
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func validateSchemaField(name string, value any, schema map[string]any) error {
	fieldType, _ := schema["type"].(string)

	switch fieldType {
	case "enum":
		values, ok := schema["values"].([]any)
		if !ok {
			return nil
		}
		str, ok := value.(string)
		if !ok {
			return &ValidationError{Field: name, Message: "expected string for enum", Value: value}
		}
		valid := false
		for _, v := range values {
			if v == str {
				valid = true
				break
			}
		}
		if !valid {
			return &ValidationError{
				Field:   name,
				Message: fmt.Sprintf("value must be one of: %v", values),
				Value:   str,
			}
		}

	case "string":
		if _, ok := value.(string); !ok {
			return &ValidationError{Field: name, Message: "expected string", Value: value}
		}

	case "integer":
		switch value.(type) {
		case float64, int, int64:
			// OK
		default:
			return &ValidationError{Field: name, Message: "expected integer", Value: value}
		}
		// Check min/max
		num := toFloat64(value)
		if min, ok := schema["min"].(float64); ok && num < min {
			return &ValidationError{
				Field:   name,
				Message: fmt.Sprintf("value must be at least %v", min),
				Value:   value,
			}
		}
		if max, ok := schema["max"].(float64); ok && num > max {
			return &ValidationError{
				Field:   name,
				Message: fmt.Sprintf("value must be at most %v", max),
				Value:   value,
			}
		}

	case "boolean":
		if _, ok := value.(bool); !ok {
			return &ValidationError{Field: name, Message: "expected boolean", Value: value}
		}

	case "array":
		val := reflect.ValueOf(value)
		if val.Kind() != reflect.Slice && val.Kind() != reflect.Array {
			return &ValidationError{Field: name, Message: "expected array", Value: value}
		}

	case "object":
		if _, ok := value.(map[string]any); !ok {
			return &ValidationError{Field: name, Message: "expected object", Value: value}
		}

	case "timestamp":
		// Accept string timestamps
		if _, ok := value.(string); !ok {
			return &ValidationError{Field: name, Message: "expected timestamp string", Value: value}
		}
	}

	return nil
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}
