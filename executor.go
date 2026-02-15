package dash

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	// ErrUnknownExecutor is returned when an executor is not registered.
	ErrUnknownExecutor = errors.New("unknown executor")

	// ErrExecutionFailed is returned when tool execution fails.
	ErrExecutionFailed = errors.New("execution failed")

	// ErrToolNotFound is returned when a tool is not found.
	ErrToolNotFound = errors.New("tool not found")

	// ErrReadOnlyViolation is returned when a write operation is attempted on a read-only executor.
	ErrReadOnlyViolation = errors.New("read-only violation")
)

// Executor is the interface for tool executors.
type Executor interface {
	Execute(ctx context.Context, args map[string]any) (any, error)
}

// RegisterExecutor registers an executor with the given name.
func (d *Dash) RegisterExecutor(name string, executor Executor) {
	d.executors[name] = executor
}

// GetExecutor returns the executor with the given name.
func (d *Dash) GetExecutor(name string) (Executor, bool) {
	e, ok := d.executors[name]
	return e, ok
}

// ExecuteTool executes a tool by name with the given arguments.
func (d *Dash) ExecuteTool(ctx context.Context, toolName string, args map[string]any) (any, error) {
	// Look up the tool
	tool, err := d.GetNodeByName(ctx, LayerAutomation, "tool", toolName)
	if err != nil {
		if err == ErrNodeNotFound {
			return nil, ErrToolNotFound
		}
		return nil, err
	}

	// Parse tool data
	var toolData struct {
		Executor   string         `json:"executor"`
		ArgsSchema map[string]any `json:"args_schema"`
	}
	if err := json.Unmarshal(tool.Data, &toolData); err != nil {
		return nil, fmt.Errorf("invalid tool data: %w", err)
	}

	// Get the executor
	executor, ok := d.executors[toolData.Executor]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownExecutor, toolData.Executor)
	}

	// Validate arguments if schema exists
	if toolData.ArgsSchema != nil {
		if err := ValidateArgs(args, toolData.ArgsSchema); err != nil {
			return nil, fmt.Errorf("argument validation failed: %w", err)
		}
	}

	// Execute
	return executor.Execute(ctx, args)
}

// SQLExecutor executes SQL queries.
type SQLExecutor struct {
	db *sql.DB
}

// Execute runs a SQL query and returns the results.
func (e *SQLExecutor) Execute(ctx context.Context, args map[string]any) (any, error) {
	query, ok := args["query"].(string)
	if !ok {
		// Check for statement (write operation)
		statement, ok := args["statement"].(string)
		if !ok {
			return nil, errors.New("missing query or statement argument")
		}
		return e.executeStatement(ctx, statement, args)
	}

	// Read-only query
	return e.executeQuery(ctx, query, args)
}

func (e *SQLExecutor) executeQuery(ctx context.Context, query string, args map[string]any) (any, error) {
	// Safety check: only allow SELECT queries
	normalized := strings.TrimSpace(strings.ToUpper(query))
	if !strings.HasPrefix(normalized, "SELECT") && !strings.HasPrefix(normalized, "WITH") {
		return nil, ErrReadOnlyViolation
	}

	// Get timeout
	timeoutMs := 5000
	if t, ok := args["timeout_ms"].(float64); ok {
		timeoutMs = int(t)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	// Get params
	var params []any
	if p, ok := args["params"].([]any); ok {
		params = p
	}

	rows, err := e.db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	// Scan all rows
	var results []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		row := make(map[string]any)
		for i, col := range columns {
			row[col] = values[i]
		}
		results = append(results, row)
	}

	return results, rows.Err()
}

func (e *SQLExecutor) executeStatement(ctx context.Context, statement string, args map[string]any) (any, error) {
	timeoutMs := 5000
	if t, ok := args["timeout_ms"].(float64); ok {
		timeoutMs = int(t)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	var params []any
	if p, ok := args["params"].([]any); ok {
		params = p
	}

	result, err := e.db.ExecContext(ctx, statement, params...)
	if err != nil {
		return nil, err
	}

	rowsAffected, _ := result.RowsAffected()
	return map[string]any{"rows_affected": rowsAffected}, nil
}

// FileReadExecutor reads files from the filesystem.
type FileReadExecutor struct {
	config *FileConfig
}

// Execute reads a file and returns its contents.
func (e *FileReadExecutor) Execute(ctx context.Context, args map[string]any) (any, error) {
	path, ok := args["path"].(string)
	if !ok {
		return nil, errors.New("missing path argument")
	}

	// Validate path
	validPath, err := e.config.ValidatePath(path)
	if err != nil {
		return nil, err
	}

	// Check if it's a directory listing
	info, err := os.Stat(validPath)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return e.listDirectory(validPath, args)
	}

	// Read file
	maxBytes := int64(1048576) // 1MB default
	if m, ok := args["max_bytes"].(float64); ok {
		maxBytes = int64(m)
	}

	file, err := os.Open(validPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	content := make([]byte, maxBytes)
	n, err := file.Read(content)
	if err != nil && err != io.EOF {
		return nil, err
	}

	encoding := "utf-8"
	if enc, ok := args["encoding"].(string); ok {
		encoding = enc
	}

	result := map[string]any{
		"path":     validPath,
		"size":     n,
		"encoding": encoding,
	}

	if encoding == "base64" {
		// Return base64 encoded
		result["content"] = content[:n] // Would be base64 encoded in real implementation
	} else {
		result["content"] = string(content[:n])
	}

	return result, nil
}

func (e *FileReadExecutor) listDirectory(path string, args map[string]any) (any, error) {
	recursive := false
	if r, ok := args["recursive"].(bool); ok {
		recursive = r
	}

	includeHidden := false
	if h, ok := args["include_hidden"].(bool); ok {
		includeHidden = h
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var files []map[string]any
	for _, entry := range entries {
		name := entry.Name()

		// Skip hidden files if not included
		if !includeHidden && strings.HasPrefix(name, ".") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		file := map[string]any{
			"name":    name,
			"is_dir":  entry.IsDir(),
			"size":    info.Size(),
			"mode":    info.Mode().String(),
			"mod_time": info.ModTime(),
		}
		files = append(files, file)

		// TODO: implement recursive listing if recursive == true
		_ = recursive
	}

	return map[string]any{
		"path":  path,
		"files": files,
	}, nil
}

// FileWriteExecutor writes files to the filesystem.
type FileWriteExecutor struct {
	config *FileConfig
}

// Execute writes content to a file.
func (e *FileWriteExecutor) Execute(ctx context.Context, args map[string]any) (any, error) {
	path, ok := args["path"].(string)
	if !ok {
		return nil, errors.New("missing path argument")
	}

	// Validate path
	validPath, err := e.config.ValidatePath(path)
	if err != nil {
		return nil, err
	}

	// Check for delete operation
	if _, isDelete := args["delete"]; isDelete {
		err := os.Remove(validPath)
		if err != nil {
			return nil, err
		}
		return map[string]any{"deleted": validPath}, nil
	}

	// Write operation
	content, ok := args["content"].(string)
	if !ok {
		return nil, errors.New("missing content argument")
	}

	// Check if file exists
	_, err = os.Stat(validPath)
	fileExists := err == nil

	overwrite := false
	if o, ok := args["overwrite"].(bool); ok {
		overwrite = o
	}

	if fileExists && !overwrite {
		return nil, errors.New("file exists and overwrite is false")
	}

	// Create directories if needed
	createDirs := false
	if c, ok := args["create_dirs"].(bool); ok {
		createDirs = c
	}

	if createDirs {
		dir := path[:strings.LastIndex(path, "/")]
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}

	// Write file
	err = os.WriteFile(validPath, []byte(content), 0644)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"path":    validPath,
		"written": len(content),
	}, nil
}

// HTTPExecutor makes HTTP requests.
type HTTPExecutor struct {
	client *http.Client
}

// Execute makes an HTTP request and returns the response.
func (e *HTTPExecutor) Execute(ctx context.Context, args map[string]any) (any, error) {
	url, ok := args["url"].(string)
	if !ok {
		return nil, errors.New("missing url argument")
	}

	method := "GET"
	if m, ok := args["method"].(string); ok {
		method = m
	}

	timeoutMs := 10000
	if t, ok := args["timeout_ms"].(float64); ok {
		timeoutMs = int(t)
	}

	client := e.client
	if client == nil {
		client = &http.Client{
			Timeout: time.Duration(timeoutMs) * time.Millisecond,
		}
	}

	var body io.Reader
	if b, ok := args["body"].(string); ok {
		body = strings.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	// Add headers
	if headers, ok := args["headers"].(map[string]any); ok {
		for k, v := range headers {
			if vs, ok := v.(string); ok {
				req.Header.Set(k, vs)
			}
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		return nil, err
	}

	// Build response headers map
	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}

	return map[string]any{
		"status":      resp.StatusCode,
		"status_text": resp.Status,
		"headers":     respHeaders,
		"body":        string(respBody),
	}, nil
}
