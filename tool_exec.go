package dash

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func defExec() *ToolDef {
	return &ToolDef{
		Name:        "exec",
		Description: "Execute a shell command. Returns stdout, stderr, and exit code.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"command"},
			"properties": map[string]any{
				"command":    map[string]any{"type": "string", "description": "Shell command to execute (passed to sh -c)"},
				"timeout_ms": map[string]any{"type": "integer", "description": "Timeout in milliseconds (default: 30000, max: 300000)"},
				"cwd":        map[string]any{"type": "string", "description": "Working directory for the command"},
			},
		},
		Tags: []string{"write", "fs"},
		Fn:   toolExec,
	}
}

func toolExec(ctx context.Context, d *Dash, args map[string]any) (any, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}

	timeoutMs := 30000
	if t, ok := args["timeout_ms"].(float64); ok {
		timeoutMs = int(t)
		if timeoutMs > 300000 {
			timeoutMs = 300000
		}
		if timeoutMs < 100 {
			timeoutMs = 100
		}
	}

	cwd := strings.TrimSuffix(d.fileConfig.AllowedRoot, "/")
	if c, ok := args["cwd"].(string); ok && c != "" {
		validated, err := d.fileConfig.ValidatePath(c)
		if err != nil {
			return nil, fmt.Errorf("invalid cwd: %w", err)
		}
		cwd = validated
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", command)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	durationMs := int(time.Since(start).Milliseconds())

	timedOut := execCtx.Err() == context.DeadlineExceeded

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if timedOut {
			exitCode = -1
		} else {
			return nil, fmt.Errorf("exec: %w", err)
		}
	}

	// Truncate output if too large (1MB per stream)
	const maxOutput = 1024 * 1024
	stdoutStr := stdout.String()
	stderrStr := stderr.String()
	if len(stdoutStr) > maxOutput {
		stdoutStr = stdoutStr[:maxOutput] + "\n... (truncated)"
	}
	if len(stderrStr) > maxOutput {
		stderrStr = stderrStr[:maxOutput] + "\n... (truncated)"
	}

	return map[string]any{
		"exit_code":   exitCode,
		"stdout":      stdoutStr,
		"stderr":      stderrStr,
		"timed_out":   timedOut,
		"duration_ms": durationMs,
	}, nil
}
