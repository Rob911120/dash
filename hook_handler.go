package dash

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProcessHookEvent handles all Claude Code hook events.
// Returns HookOutput with content to inject to stdout (for SessionStart).
func (d *Dash) ProcessHookEvent(ctx context.Context, input []byte) (*HookOutput, error) {
	var cc ClaudeCodeInput
	if err := json.Unmarshal(input, &cc); err != nil {
		return nil, err
	}

	switch cc.HookEventName {
	case HookSessionStart:
		return d.handleSessionStart(ctx, &cc)
	case HookPreToolUse:
		return d.handlePreToolUse(ctx, &cc)
	case HookPostToolUse:
		return nil, d.handlePostToolUse(ctx, &cc)
	case HookPostToolUseFailure:
		return nil, d.handlePostToolUseFailure(ctx, &cc)
	case HookSessionEnd:
		return nil, d.handleSessionEnd(ctx, &cc)
	default:
		// Ignore unknown events
		return nil, nil
	}
}

func (d *Dash) handleSessionStart(ctx context.Context, cc *ClaudeCodeInput) (*HookOutput, error) {
	now := time.Now()

	// Capture system state as baseline for the session
	sysState := CaptureSystemState()
	procCtx := CaptureProcessContext()

	// Create or update session node
	sessionData := map[string]any{
		"status":          "active",
		"started_at":      now.Format(time.RFC3339),
		"source":          cc.Source,
		"model":           cc.Model,
		"cwd":             cc.Cwd,
		"permission_mode": cc.PermissionMode,
		"system_baseline": sysState,
		"process":         procCtx,
	}

	session, err := d.GetOrCreateNode(ctx, LayerContext, "session", cc.SessionID, sessionData)
	if err != nil {
		return nil, err
	}

	// Build envelope for observation
	envelope := d.buildEnvelope(cc, "session.start")
	envelope.SystemState = sysState
	envelope.ProcessContext = procCtx
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}

	// Save observation
	err = d.CreateObservation(ctx, &Observation{
		NodeID:     session.ID,
		Type:       "session_event",
		Data:       envelopeJSON,
		ObservedAt: now,
	})
	if err != nil {
		return nil, err
	}

	// Generate the default prompt context and inject to stdout
	content, _ := d.GetPrompt(ctx, "default", PromptOptions{
		Cwd:       cc.Cwd,
		SessionID: cc.SessionID,
	})
	if content != "" {
		return &HookOutput{Content: content}, nil
	}

	return nil, nil
}

func (d *Dash) handlePreToolUse(ctx context.Context, cc *ClaudeCodeInput) (*HookOutput, error) {
	now := time.Now()

	// Get or create session node
	session, err := d.GetOrCreateNode(ctx, LayerContext, "session", cc.SessionID, map[string]any{
		"status": "active",
	})
	if err != nil {
		return nil, err
	}

	// Build envelope
	envelope := d.buildEnvelope(cc, "tool.pre")
	envelope.Normalized.Subject = d.extractSubject(cc)
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}

	// Save observation
	err = d.CreateObservation(ctx, &Observation{
		NodeID:     session.ID,
		Type:       "tool_event",
		Data:       envelopeJSON,
		ObservedAt: now,
	})
	if err != nil {
		return nil, err
	}

	// Check for past failures (non-blocking - errors don't stop the tool)
	failureCheck, checkErr := d.CheckPastFailures(ctx, cc.ToolName, cc.ToolInput)
	if checkErr == nil && failureCheck != nil && failureCheck.HasFailures {
		return &HookOutput{
			SystemMessage: failureCheck.Warning,
			IsJSON:        true,
		}, nil
	}

	return nil, nil
}

func (d *Dash) handlePostToolUse(ctx context.Context, cc *ClaudeCodeInput) error {
	now := time.Now()

	// Calculate duration from PreToolUse event
	var durationMs *int
	if cc.ToolUseID != "" {
		preTime, _ := d.GetPreToolUseTime(ctx, cc.ToolUseID)
		if !preTime.IsZero() {
			ms := int(now.Sub(preTime).Milliseconds())
			durationMs = &ms
		}
	}

	// Capture system state
	sysState := CaptureSystemState()
	procCtx := CaptureProcessContext()

	// Capture file metadata for file operations
	var fileMeta *FileMetadata
	filePath := extractFilePath(cc.ToolInput)
	if isFileOperation(cc.ToolName) && filePath != "" {
		fileMeta = CaptureFileMetadata(filePath, isWriteOperation(cc.ToolName))
	}

	// Get or create session node
	session, err := d.GetOrCreateNode(ctx, LayerContext, "session", cc.SessionID, map[string]any{
		"status": "active",
	})
	if err != nil {
		return err
	}

	// Create edge_event for file operations with enriched data
	if isFileOperation(cc.ToolName) && filePath != "" {
		fileNode, fileErr := d.GetOrCreateNode(ctx, LayerSystem, "file", filePath, map[string]any{
			"path": filePath,
		})
		if fileErr == nil && fileNode != nil {
			eventData, _ := json.Marshal(map[string]any{
				"tool_use_id": cc.ToolUseID,
				"tool_name":   cc.ToolName,
				"duration_ms": durationMs,
				"file":        fileMeta,
				"system":      sysState,
				"process":     procCtx,
			})
			d.CreateEdgeEvent(ctx, &EdgeEvent{
				SourceID:   session.ID,
				TargetID:   fileNode.ID,
				Relation:   determineRelation(cc.ToolName),
				Success:    true,
				DurationMs: durationMs,
				Data:       eventData,
				OccurredAt: now,
			})

			// Generate embedding + summary for write operations (async, non-blocking)
			if isWriteOperation(cc.ToolName) && fileMeta != nil && fileMeta.Hash != "" {
				go d.maybeUpdateEmbedding(fileNode, filePath, fileMeta.Hash)
				go d.maybeUpdateSummary(fileNode, filePath, fileMeta.Hash)
			}

			// Link active task to modified file (best-effort)
			if isWriteOperation(cc.ToolName) {
				_ = d.LinkActiveTaskToFile(ctx, fileNode.ID)
			}
		}
	}

	// Build envelope with system awareness
	envelope := d.buildEnvelope(cc, "tool.post")
	envelope.Normalized.Subject = d.extractSubject(cc)
	envelope.Normalized.Outcome = &Outcome{
		Success:    boolPtr(true),
		DurationMs: durationMs,
	}
	envelope.SystemState = sysState
	envelope.ProcessContext = procCtx
	envelope.FileMetadata = fileMeta
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return err
	}

	// Save observation
	return d.CreateObservation(ctx, &Observation{
		NodeID:     session.ID,
		Type:       "tool_event",
		Data:       envelopeJSON,
		ObservedAt: now,
	})
}

func (d *Dash) handlePostToolUseFailure(ctx context.Context, cc *ClaudeCodeInput) error {
	now := time.Now()

	// Get or create session node
	session, err := d.GetOrCreateNode(ctx, LayerContext, "session", cc.SessionID, map[string]any{
		"status": "active",
	})
	if err != nil {
		return err
	}

	// Create edge_event for file operations (even on failure)
	if isFileOperation(cc.ToolName) {
		filePath := extractFilePath(cc.ToolInput)
		if filePath != "" {
			fileNode, fileErr := d.GetOrCreateNode(ctx, LayerSystem, "file", filePath, map[string]any{
				"path": filePath,
			})
			if fileErr == nil && fileNode != nil {
				eventData, _ := json.Marshal(map[string]any{
					"tool_use_id": cc.ToolUseID,
					"tool_name":   cc.ToolName,
					"error":       cc.Error,
				})
				d.CreateEdgeEvent(ctx, &EdgeEvent{
					SourceID:   session.ID,
					TargetID:   fileNode.ID,
					Relation:   EventRelationFailedWith,
					Success:    false,
					Data:       eventData,
					OccurredAt: now,
				})
			}
		}
	}

	// Build envelope
	envelope := d.buildEnvelope(cc, "tool.failure")
	envelope.Normalized.Subject = d.extractSubject(cc)
	envelope.Normalized.Outcome = &Outcome{
		Success: boolPtr(false),
		Error:   cc.Error,
	}
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return err
	}

	// Save observation
	return d.CreateObservation(ctx, &Observation{
		NodeID:     session.ID,
		Type:       "tool_event",
		Data:       envelopeJSON,
		ObservedAt: now,
	})
}

func (d *Dash) handleSessionEnd(ctx context.Context, cc *ClaudeCodeInput) error {
	now := time.Now()

	// Get existing session node
	session, err := d.GetNodeByName(ctx, LayerContext, "session", cc.SessionID)
	if err != nil {
		// Session might not exist, create it with ended status
		session, err = d.GetOrCreateNode(ctx, LayerContext, "session", cc.SessionID, map[string]any{
			"status":     "ended",
			"ended_at":   now.Format(time.RFC3339),
			"end_reason": cc.Reason,
		})
		if err != nil {
			return err
		}
	} else {
		// Update existing session with end data
		endData := map[string]any{
			"status":     "ended",
			"ended_at":   now.Format(time.RFC3339),
			"end_reason": cc.Reason,
		}

		// Count edge_events for this session to determine promotion candidacy
		var eventCount int
		if err := d.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM edge_events WHERE source_id = $1`,
			session.ID,
		).Scan(&eventCount); err == nil && eventCount > 10 {
			endData["promotion_candidate"] = true
			endData["event_count"] = eventCount
		}

		err = d.UpdateNodeData(ctx, session, endData)
		if err != nil {
			return err
		}
	}

	// Calculate richness score + auto-promotion insights (non-blocking, best-effort)
	go func() {
		scoreCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		score, breakdown, err := d.CalculateRichnessScore(scoreCtx, session.ID)
		if err != nil {
			return
		}
		updates := map[string]any{
			"richness_score":  score,
			"score_breakdown": breakdown,
		}
		// Generate and auto-promote insights for high-value sessions
		if score >= 40 {
			updates["promotion_candidate"] = true
			if suggestions, err := d.SuggestInsights(scoreCtx, session.ID); err == nil && len(suggestions) > 0 {
				updates["suggested_insights"] = suggestions
				// Auto-promote: create permanent CONTEXT.insight nodes
				promoted := d.autoPromoteInsights(scoreCtx, session.ID, suggestions)
				updates["auto_promoted"] = promoted
			}
		}
		_ = d.UpdateNodeData(scoreCtx, session, updates)
	}()

	// Build envelope for observation
	envelope := d.buildEnvelope(cc, "session.end")
	envelopeJSON, err := json.Marshal(envelope)
	if err != nil {
		return err
	}

	// Save observation
	return d.CreateObservation(ctx, &Observation{
		NodeID:     session.ID,
		Type:       "session_event",
		Data:       envelopeJSON,
		ObservedAt: now,
	})
}

// buildEnvelope creates a DashHookEnvelope from Claude Code input.
func (d *Dash) buildEnvelope(cc *ClaudeCodeInput, event string) *DashHookEnvelope {
	envelope := &DashHookEnvelope{
		EnvelopeVersion: "dashhook/v1",
		ReceivedAt:      time.Now(),
		ClaudeCode:      cc,
		Normalized: &NormalizedEvent{
			Event:         event,
			CorrelationID: cc.ToolUseID,
		},
	}

	// Add tool reference for tool events
	if cc.ToolName != "" {
		envelope.Normalized.Tool = &ToolRef{
			Name: cc.ToolName,
			Kind: getToolKind(cc.ToolName),
		}
	}

	return envelope
}

// extractSubject extracts the subject reference from tool input.
func (d *Dash) extractSubject(cc *ClaudeCodeInput) *SubjectRef {
	if cc.ToolInput == nil {
		return nil
	}

	var input map[string]any
	if err := json.Unmarshal(cc.ToolInput, &input); err != nil {
		return nil
	}

	switch cc.ToolName {
	case "Read", "Write", "Edit", "View", "MultiEdit":
		if path, ok := input["file_path"].(string); ok {
			return &SubjectRef{Kind: "file", Ref: path}
		}
	case "Glob":
		if pattern, ok := input["pattern"].(string); ok {
			return &SubjectRef{Kind: "pattern", Ref: pattern}
		}
	case "Grep":
		if pattern, ok := input["pattern"].(string); ok {
			return &SubjectRef{Kind: "pattern", Ref: pattern}
		}
	case "LS":
		if path, ok := input["path"].(string); ok {
			return &SubjectRef{Kind: "directory", Ref: path}
		}
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			return &SubjectRef{Kind: "command", Ref: cmd}
		}
	case "WebFetch":
		if url, ok := input["url"].(string); ok {
			return &SubjectRef{Kind: "url", Ref: url}
		}
	case "WebSearch":
		if query, ok := input["query"].(string); ok {
			return &SubjectRef{Kind: "query", Ref: query}
		}
	case "Task":
		if prompt, ok := input["prompt"].(string); ok {
			// Truncate long prompts
			if len(prompt) > 100 {
				prompt = prompt[:100] + "..."
			}
			return &SubjectRef{Kind: "task", Ref: prompt}
		}
	}

	return nil
}

// isFileOperation returns true if the tool operates on a specific file.
// Search tools (Glob, Grep, LS) are excluded — they operate on patterns/directories,
// not individual files, and would create junk SYSTEM.file nodes.
func isFileOperation(toolName string) bool {
	switch toolName {
	case "Read", "Write", "Edit", "View", "MultiEdit":
		return true
	default:
		return false
	}
}

// determineRelation returns the appropriate event relation for a tool.
func determineRelation(toolName string) EventRelation {
	switch toolName {
	case "Read", "View", "Glob", "Grep", "LS":
		return EventRelationObserved
	case "Write", "Edit", "MultiEdit":
		return EventRelationModified
	default:
		return EventRelationTriggered
	}
}

// extractFilePath extracts the file path from tool input.
// Returns empty string for paths that are not real files (glob patterns, directories).
func extractFilePath(input json.RawMessage) string {
	if input == nil {
		return ""
	}

	var data map[string]any
	if err := json.Unmarshal(input, &data); err != nil {
		return ""
	}

	// Try file_path first (most common)
	if path, ok := data["file_path"].(string); ok {
		return validateFilePath(path)
	}

	// Try path
	if path, ok := data["path"].(string); ok {
		return validateFilePath(path)
	}

	return ""
}

// validateFilePath rejects paths that are not real files.
func validateFilePath(path string) string {
	if path == "" {
		return ""
	}
	// Reject glob patterns
	if strings.ContainsAny(path, "*?[") {
		return ""
	}
	// Reject bare directories (no extension, no filename with dot)
	// This catches /dash, /dash/cmd, etc.
	base := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		base = path[idx+1:]
	}
	if !strings.Contains(base, ".") && base != "" {
		// Could be a directory. Check if it looks like a common dir path.
		// We allow extensionless files like Makefile, Dockerfile, etc.
		knownExtensionless := map[string]bool{
			"Makefile": true, "Dockerfile": true, "Vagrantfile": true,
			"Gemfile": true, "Rakefile": true, "Procfile": true,
			"LICENSE": true, "README": true, "CHANGELOG": true,
		}
		if !knownExtensionless[base] {
			return ""
		}
	}
	return path
}

// getToolKind returns the tool kind for categorization.
func getToolKind(toolName string) ToolKind {
	switch toolName {
	case "Read", "Write", "Edit", "Glob", "Grep", "LS", "View", "MultiEdit":
		return ToolKindFilesystem
	case "Bash":
		return ToolKindShell
	case "WebFetch", "WebSearch":
		return ToolKindWeb
	case "Task", "Dispatch":
		return ToolKindTask
	default:
		if strings.HasPrefix(toolName, "mcp__") {
			return ToolKindMCP
		}
		return ToolKindOther
	}
}

// maybeUpdateEmbedding checks if embedding needs update and generates it async.
// This is called in a goroutine and must not block the hook response.
func (d *Dash) maybeUpdateEmbedding(fileNode *Node, filePath, newHash string) {
	// Check if hash has changed
	existingHash, _ := d.GetNodeContentHash(context.Background(), fileNode.ID)
	if existingHash == newHash {
		// Hash unchanged, no need to regenerate embedding
		return
	}

	// Read file content for embedding
	content, err := readFileForEmbedding(filePath)
	if err != nil || content == "" {
		return
	}

	// Generate embedding
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	embedding, err := d.embedder.Embed(ctx, content)
	if err != nil || embedding == nil {
		return
	}

	// Update node with embedding
	d.UpdateNodeEmbedding(ctx, fileNode.ID, embedding, newHash)
}

// maybeUpdateSummary checks if summary needs update and generates it async.
// This is called in a goroutine and must not block the hook response.
func (d *Dash) maybeUpdateSummary(fileNode *Node, filePath, newHash string) {
	// Check if node already has a summary for this hash
	var existing map[string]any
	if err := json.Unmarshal(fileNode.Data, &existing); err == nil {
		if summaryHash, ok := existing["summary_hash"].(string); ok && summaryHash == newHash {
			return
		}
	}

	// Read file content
	content, err := readFileForEmbedding(filePath)
	if err != nil || content == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	summary, err := d.summarizer.Summarize(ctx, content, filePath)
	if err != nil || summary == "" {
		return
	}

	d.UpdateNodeData(ctx, fileNode, map[string]any{
		"summary":      summary,
		"summary_at":   time.Now().Format(time.RFC3339),
		"summary_hash": newHash,
	})
}

// readFileForEmbedding reads file content for embedding generation.
// Returns empty string if file is too large, binary, or unreadable.
func readFileForEmbedding(filePath string) (string, error) {
	// Check if file is suitable for embedding
	if !isEmbeddableFile(filePath) {
		return "", nil
	}

	// Read file content (limited to MaxEmbeddingTextSize)
	content, err := readFileContent(filePath, MaxEmbeddingTextSize)
	if err != nil {
		return "", err
	}

	// Skip likely binary content
	if isBinaryContent(content) {
		return "", nil
	}

	return content, nil
}

// isEmbeddableFile checks if a file is suitable for embedding based on extension.
func isEmbeddableFile(path string) bool {
	// Skip known binary extensions
	binaryExtensions := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".bin": true, ".dat": true, ".db": true, ".sqlite": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".ico": true,
		".mp3": true, ".mp4": true, ".avi": true, ".mov": true, ".wav": true,
		".zip": true, ".tar": true, ".gz": true, ".7z": true, ".rar": true,
		".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
		".wasm": true, ".pyc": true, ".class": true,
	}

	for ext := range binaryExtensions {
		if strings.HasSuffix(strings.ToLower(path), ext) {
			return false
		}
	}
	return true
}

// isBinaryContent checks if content appears to be binary (contains null bytes).
func isBinaryContent(content string) bool {
	// Check first 8KB for null bytes
	checkLen := len(content)
	if checkLen > 8192 {
		checkLen = 8192
	}
	for i := 0; i < checkLen; i++ {
		if content[i] == 0 {
			return true
		}
	}
	return false
}

// readFileContent reads up to maxBytes from a file.
func readFileContent(path string, maxBytes int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Check file size first
	info, err := f.Stat()
	if err != nil {
		return "", err
	}

	// Skip empty files
	if info.Size() == 0 {
		return "", nil
	}

	// Limit read size
	readSize := int(info.Size())
	if maxBytes > 0 && readSize > maxBytes {
		readSize = maxBytes
	}

	buf := make([]byte, readSize)
	n, err := f.Read(buf)
	if err != nil && err.Error() != "EOF" {
		return "", err
	}

	return string(buf[:n]), nil
}

// autoPromoteInsights creates permanent CONTEXT.insight nodes from session suggestions.
// Returns the number of successfully promoted insights.
func (d *Dash) autoPromoteInsights(ctx context.Context, sessionID uuid.UUID, suggestions []map[string]string) int {
	promoted := 0
	for _, s := range suggestions {
		text := s["text"]
		if text == "" {
			continue
		}

		name := text
		if len(name) > 255 {
			name = name[:252] + "..."
		}

		// Skip if insight with this name already exists
		if existing, _ := d.GetNodeByName(ctx, LayerContext, "insight", name); existing != nil {
			continue
		}

		data := map[string]any{
			"text":       text,
			"type":       s["type"],
			"context":    s["context"],
			"created_by": "auto-promotion",
		}
		dataJSON, err := json.Marshal(data)
		if err != nil {
			continue
		}

		node := &Node{
			Layer: LayerContext,
			Type:  "insight",
			Name:  name,
			Data:  dataJSON,
		}
		if err := d.CreateNode(ctx, node); err != nil {
			continue
		}

		// Embed the insight async (best-effort)
		go d.EmbedNode(context.Background(), node)

		// Link insight --derived_from--> session
		_ = d.CreateEdge(ctx, &Edge{
			SourceID: node.ID,
			TargetID: sessionID,
			Relation: RelationDerivedFrom,
		})
		promoted++
	}
	return promoted
}
