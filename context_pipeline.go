package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SourceFunc generates one section of a system prompt.
type SourceFunc func(p SourceParams) string

// SourceParams carries all data a source function might need.
type SourceParams struct {
	Ctx          context.Context
	D            *Dash
	Cwd          string
	SessionID    string
	TaskName     string
	SuggName     string
	PlanName     string
	AgentKey     string // agent identifier for agent-continuous profile
	AgentMission string // why this agent was spawned
	MaxItems     int
	Format       string // "rich" | "compact"
}

// Pipeline declares what a system prompt should contain.
type Pipeline struct {
	Instruction string           `json:"instruction,omitempty"`
	Sources     []PipelineSource `json:"sources"`
}

// PipelineSource references a named source with optional overrides.
type PipelineSource struct {
	Name     string `json:"name"`
	MaxItems int    `json:"max_items,omitempty"`
	Format   string `json:"format,omitempty"`
}

// sourceRegistry maps source names to their implementations.
var sourceRegistry = map[string]SourceFunc{
	"header":            srcHeader,
	"mission":           srcMission,
	"now":               srcNow,
	"tasks":             srcTasks,
	"constraints":       srcConstraints,
	"insights":          srcInsights,
	"decisions":         srcDecisions,
	"files":             srcFiles,
	"suggestions":       srcSuggestions,
	"promote":           srcPromote,
	"session":           srcSession,
	"task_detail":       srcTaskDetail,
	"suggestion_detail": srcSuggestionDetail,
	"sibling_tasks":     srcSiblingTasks,
	"context_pack":      srcContextPack,
	"plan_execution":    srcPlanExecution,
	// Agent-continuous sources
	"agent_envelope":     srcAgentEnvelope,
	"recent_decisions":   srcRecentDecisions,
	"pending_decisions":  srcPendingDecisions,
	"active_agents":      srcActiveAgents,
	// Orchestrator sources
	"work_orders":        srcWorkOrders,
	"pipeline_status":    srcPipelineStatus,
	"active_work_order":  srcActiveWorkOrder,
}

// RunPipeline executes a pipeline and returns the concatenated text.
func (d *Dash) RunPipeline(ctx context.Context, p Pipeline, params SourceParams) string {
	params.Ctx = ctx
	params.D = d
	var b strings.Builder
	for _, src := range p.Sources {
		fn := sourceRegistry[src.Name]
		if fn == nil {
			continue
		}
		// Apply per-source overrides
		sp := params
		if src.MaxItems > 0 {
			sp.MaxItems = src.MaxItems
		}
		if src.Format != "" {
			sp.Format = src.Format
		}
		if section := fn(sp); section != "" {
			b.WriteString(section)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// --- Source functions ---

func srcHeader(p SourceParams) string {
	var parts []string

	// Project info
	if p.Cwd != "" {
		timeout := 2 * time.Second
		qCtx, cancel := context.WithTimeout(p.Ctx, timeout)
		defer cancel()
		var name, path string
		err := p.D.db.QueryRowContext(qCtx, queryGetProjectByPath, p.Cwd).Scan(&name, &path)
		if err == nil {
			parts = append(parts, fmt.Sprintf("PROJECT: %s", name))
			parts = append(parts, fmt.Sprintf("PATH: %s", path))
		}
	}

	// Git status
	if p.Cwd != "" {
		if gs := GetGitStatus(p.Cwd); gs != nil {
			gitLine := gs.Branch
			if gs.Uncommitted > 0 {
				gitLine += fmt.Sprintf(" (%d uncommitted)", gs.Uncommitted)
			}
			parts = append(parts, fmt.Sprintf("GIT: %s", gitLine))
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " | ") + "\n"
}

func srcMission(p SourceParams) string {
	node, err := p.D.querySingleNode(p.Ctx, queryGetMission, 2*time.Second)
	if err != nil || node == nil {
		return ""
	}
	data := extractNodeData(node)
	// Try statement first (mission nodes), then description
	if stmt, ok := data["statement"].(string); ok && stmt != "" {
		return fmt.Sprintf("\nMISSION: %s\n", stmt)
	}
	if desc, ok := data["description"].(string); ok && desc != "" {
		return fmt.Sprintf("\nMISSION: %s\n", desc)
	}
	return ""
}

func srcNow(p SourceParams) string {
	node, err := p.D.querySingleNode(p.Ctx, queryGetContextFrame, 2*time.Second)
	if err != nil || node == nil {
		return ""
	}
	data := extractNodeData(node)

	var b strings.Builder
	if focus, ok := data["current_focus"].(string); ok && focus != "" {
		b.WriteString(fmt.Sprintf("\nNOW: %s\n", focus))
	}
	if nextSteps, ok := data["next_steps"].([]any); ok && len(nextSteps) > 0 {
		parts := make([]string, 0, len(nextSteps))
		for _, s := range nextSteps {
			if str, ok := s.(string); ok {
				parts = append(parts, str)
			}
		}
		if len(parts) > 0 {
			b.WriteString(fmt.Sprintf("NEXT: %s\n", strings.Join(parts, ", ")))
		}
	}
	if blockers, ok := data["blockers"].([]any); ok && len(blockers) > 0 {
		parts := make([]string, 0, len(blockers))
		for _, s := range blockers {
			if str, ok := s.(string); ok {
				parts = append(parts, str)
			}
		}
		if len(parts) > 0 {
			b.WriteString(fmt.Sprintf("BLOCKERS: %s\n", strings.Join(parts, ", ")))
		}
	} else {
		b.WriteString("BLOCKERS: none\n")
	}
	return b.String()
}

func srcTasks(p SourceParams) string {
	tasks, err := p.D.GetActiveTasksWithDeps(p.Ctx)
	if err != nil || len(tasks) == 0 {
		return ""
	}

	maxItems := p.MaxItems
	if maxItems > 0 && len(tasks) > maxItems {
		tasks = tasks[:maxItems]
	}

	format := p.Format
	if format == "" {
		format = "rich"
	}

	var b strings.Builder
	b.WriteString("\nACTIVE:\n")
	for _, t := range tasks {
		data := extractNodeData(t.Node)
		status := t.Status
		if t.IsBlocked {
			status += "|blocked"
		}

		if format == "rich" {
			intentPart := ""
			if t.Intent != "" {
				intentPart = fmt.Sprintf(" (intent: %s)", t.Intent)
			}
			b.WriteString(fmt.Sprintf("- %s [%s]%s\n", t.Node.Name, status, intentPart))

			if len(t.Blocks) > 0 {
				b.WriteString(fmt.Sprintf("  blocks: %s\n", strings.Join(t.Blocks, ", ")))
			}
			if len(t.BlockedBy) > 0 {
				b.WriteString(fmt.Sprintf("  blocked_by: %s\n", strings.Join(t.BlockedBy, ", ")))
			}
			desc, _ := data["description"].(string)
			if desc != "" {
				if len(desc) > 70 {
					desc = desc[:67] + "..."
				}
				b.WriteString(fmt.Sprintf("  \"%s\"\n", desc))
			}
		} else {
			statement, _ := data["statement"].(string)
			if statement == "" {
				statement, _ = data["description"].(string)
			}
			if statement == "" {
				statement = t.Node.Name
			}
			if len(statement) > 60 {
				statement = statement[:57] + "..."
			}
			b.WriteString(fmt.Sprintf("- %s [%s]\n", statement, status))
		}
	}
	return b.String()
}

func srcConstraints(p SourceParams) string {
	nodes, err := p.D.queryMultipleNodes(p.Ctx, queryGetConstraints, 2*time.Second)
	if err != nil || len(nodes) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\nCONSTRAINTS:\n")
	for _, c := range nodes {
		data := extractNodeData(c)
		if text, ok := data["text"].(string); ok {
			if len(text) > 70 {
				text = text[:67] + "..."
			}
			b.WriteString(fmt.Sprintf("- %s\n", text))
		}
	}
	return b.String()
}

func srcInsights(p SourceParams) string {
	nodes, err := p.D.queryMultipleNodes(p.Ctx, queryGetRecentInsights, 2*time.Second)
	if err != nil || len(nodes) == 0 {
		return ""
	}

	maxItems := p.MaxItems
	if maxItems > 0 && len(nodes) > maxItems {
		nodes = nodes[:maxItems]
	}

	var b strings.Builder
	b.WriteString("\nINSIGHTS (recent):\n")
	for _, n := range nodes {
		data := extractNodeData(n)
		text, _ := data["text"].(string)
		if text == "" {
			text = n.Name
		}
		if len(text) > 70 {
			text = text[:67] + "..."
		}
		b.WriteString(fmt.Sprintf("- %s\n", text))
	}
	return b.String()
}

func srcDecisions(p SourceParams) string {
	nodes, err := p.D.queryMultipleNodes(p.Ctx, queryGetRecentDecisions, 2*time.Second)
	if err != nil || len(nodes) == 0 {
		return ""
	}

	maxItems := p.MaxItems
	if maxItems > 0 && len(nodes) > maxItems {
		nodes = nodes[:maxItems]
	}

	var b strings.Builder
	b.WriteString("\nDECISIONS:\n")
	for _, n := range nodes {
		data := extractNodeData(n)
		text, _ := data["text"].(string)
		if text == "" {
			text = n.Name
		}
		if len(text) > 120 {
			text = text[:117] + "..."
		}
		b.WriteString(fmt.Sprintf("- %s\n", text))
	}
	return b.String()
}

func srcFiles(p SourceParams) string {
	files, err := p.D.getRecentFileActivity(p.Ctx)
	if err != nil || len(files) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\nRECENT FILES (2h):\n")
	for _, file := range files {
		displayPath := file.FilePath
		if len(displayPath) > 50 {
			displayPath = "..." + filepath.Base(displayPath)
		}
		b.WriteString(fmt.Sprintf("- %s [%s]\n", displayPath, file.Relation))
	}
	return b.String()
}

func srcSuggestions(p SourceParams) string {
	// Build context data to get suggestions
	cd := &ContextData{}
	if files, err := p.D.getRecentFileActivity(p.Ctx); err == nil {
		cd.RecentFiles = files
	}
	suggestions, err := p.D.generateSuggestions(p.Ctx, cd)
	if err != nil || len(suggestions) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\nSUGGESTIONS:\n")
	for _, s := range suggestions {
		b.WriteString(fmt.Sprintf("- %s (%s)\n", s.Text, s.Reason))
	}
	return b.String()
}

func srcPromote(p SourceParams) string {
	nodes, err := p.D.queryMultipleNodes(p.Ctx, queryGetPromotionCandidates, 2*time.Second)
	if err != nil || len(nodes) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n")
	for _, pc := range nodes {
		data := extractNodeData(pc)
		eventCount, _ := data["event_count"].(float64)
		ago := formatTimeAgo(pc.UpdatedAt)
		b.WriteString(fmt.Sprintf("PROMOTE? session %s (%d events, %s)\n", pc.Name, int(eventCount), ago))
	}
	return b.String()
}

func srcSession(p SourceParams) string {
	prev, err := p.D.getPreviousSession(p.Ctx, p.SessionID)
	if err != nil || prev == nil || prev.EndedAt.IsZero() {
		return ""
	}

	ago := formatTimeAgo(prev.EndedAt)
	duration := formatDuration(prev.EndedAt.Sub(prev.StartedAt))
	return fmt.Sprintf("\nLAST SESSION: %s, %s, %d files\n", ago, duration, prev.FilesCount)
}

func srcTaskDetail(p SourceParams) string {
	if p.TaskName == "" {
		return ""
	}

	allTasks, err := p.D.GetActiveTasksWithDeps(p.Ctx)
	if err != nil {
		return ""
	}

	var task *TaskWithDeps
	for i, t := range allTasks {
		if t.Node.Name == p.TaskName {
			task = &allTasks[i]
			break
		}
	}
	if task == nil {
		return ""
	}

	var b strings.Builder
	data := extractNodeData(task.Node)
	desc, _ := data["description"].(string)
	b.WriteString(fmt.Sprintf("TASK: %s [%s]\n", task.Node.Name, task.Status))
	if desc != "" {
		b.WriteString(fmt.Sprintf("  %s\n", desc))
	}
	if task.Intent != "" {
		b.WriteString(fmt.Sprintf("  Intent: %s\n", task.Intent))
	}
	b.WriteString("\n")

	if len(task.Blocks) > 0 {
		b.WriteString(fmt.Sprintf("BLOCKS: %s\n", strings.Join(task.Blocks, ", ")))
	}
	if len(task.BlockedBy) > 0 {
		b.WriteString(fmt.Sprintf("BLOCKED BY: %s\n", strings.Join(task.BlockedBy, ", ")))
	}
	if len(task.Blocks) > 0 || len(task.BlockedBy) > 0 {
		b.WriteString("\n")
	}
	return b.String()
}

func srcSuggestionDetail(p SourceParams) string {
	if p.SuggName == "" {
		return ""
	}

	node, err := p.D.GetNodeByName(p.Ctx, LayerContext, "suggestion", p.SuggName)
	if err != nil {
		return ""
	}

	var data map[string]any
	if err := json.Unmarshal(node.Data, &data); err != nil {
		return ""
	}

	name := pipelineGetString(data, "name")
	desc := pipelineGetString(data, "description")
	intent := pipelineGetString(data, "intent")
	reason := pipelineGetString(data, "reason")
	source := pipelineGetString(data, "source")
	alignment := 0
	if pct, ok := data["alignment_pct"].(float64); ok {
		alignment = int(pct)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("SUGGESTION: %s\n", name))
	if desc != "" {
		b.WriteString(fmt.Sprintf("  %s\n", desc))
	}
	if intent != "" {
		b.WriteString(fmt.Sprintf("  Intent: %s (alignment: %d%%)\n", intent, alignment))
	}
	if reason != "" {
		b.WriteString(fmt.Sprintf("  Reason: %s\n", reason))
	}
	b.WriteString(fmt.Sprintf("  Source: %s\n\n", source))
	return b.String()
}

func srcSiblingTasks(p SourceParams) string {
	// Determine the intent to search for
	var targetIntent string

	if p.TaskName != "" {
		allTasks, err := p.D.GetActiveTasksWithDeps(p.Ctx)
		if err != nil {
			return ""
		}
		for _, t := range allTasks {
			if t.Node.Name == p.TaskName && t.Intent != "" {
				targetIntent = t.Intent
				break
			}
		}
	} else if p.SuggName != "" {
		node, err := p.D.GetNodeByName(p.Ctx, LayerContext, "suggestion", p.SuggName)
		if err == nil {
			var data map[string]any
			if json.Unmarshal(node.Data, &data) == nil {
				targetIntent = pipelineGetString(data, "intent")
			}
		}
	}

	if targetIntent == "" {
		return ""
	}

	allTasks, err := p.D.GetActiveTasksWithDeps(p.Ctx)
	if err != nil {
		return ""
	}

	var siblings []TaskWithDeps
	for _, t := range allTasks {
		if t.Intent == targetIntent && t.Node.Name != p.TaskName {
			siblings = append(siblings, t)
		}
	}

	if len(siblings) == 0 {
		return ""
	}

	label := "SIBLING TASKS:"
	if p.SuggName != "" {
		label = "EXISTING TASKS (same intent):"
	}

	var b strings.Builder
	b.WriteString(label + "\n")
	for _, s := range siblings {
		line := fmt.Sprintf("  - %s [%s]", s.Node.Name, s.Status)
		if s.IsBlocked {
			line += " (blocked)"
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

func srcContextPack(p SourceParams) string {
	var searchQuery string
	var taskID *uuid.UUID
	profile := ProfileDefault

	if p.TaskName != "" {
		profile = ProfileTask
		layer := LayerContext
		typ := "task"
		allTasks, _ := p.D.SearchNodes(p.Ctx, NodeFilter{Layer: &layer, Type: &typ})
		for _, t := range allTasks {
			if t.Name == p.TaskName {
				data := extractNodeData(t)
				desc, _ := data["description"].(string)
				searchQuery = p.TaskName + " " + desc
				id := t.ID
				taskID = &id
				break
			}
		}
	} else if p.SuggName != "" {
		profile = ProfileTask
		node, err := p.D.GetNodeByName(p.Ctx, LayerContext, "suggestion", p.SuggName)
		if err == nil {
			var data map[string]any
			if json.Unmarshal(node.Data, &data) == nil {
				searchQuery = pipelineGetString(data, "name") + " " + pipelineGetString(data, "description")
			}
		}
	}

	if searchQuery == "" {
		// Fallback: use card_text from context_frame as default query
		frame, err := p.D.querySingleNode(p.Ctx, queryGetContextFrame, 2*time.Second)
		if err != nil || frame == nil {
			return ""
		}
		data := extractNodeData(frame)
		if card, ok := data["card_text"].(string); ok && card != "" {
			searchQuery = card
		} else {
			return ""
		}
	}

	pack, err := p.D.AssembleContextPack(p.Ctx, searchQuery, profile, taskID)
	if err != nil || len(pack.Items) == 0 {
		return ""
	}
	return pack.RenderForPrompt()
}

// pipelineGetString extracts a string from a map, returning "" if not found.
func pipelineGetString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func srcPlanExecution(p SourceParams) string {
	if p.PlanName == "" {
		return ""
	}

	node, err := p.D.GetNodeByName(p.Ctx, LayerContext, "plan", p.PlanName)
	if err != nil {
		return ""
	}

	ps, err := parsePlanData(node)
	if err != nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("PLAN: %s\n", ps.Node.Name))
	if ps.Goal != "" {
		b.WriteString(fmt.Sprintf("GOAL: %s\n", ps.Goal))
	}
	if ps.Scope != "" {
		b.WriteString(fmt.Sprintf("SCOPE: %s\n", ps.Scope))
	}
	b.WriteString("\n")

	// Steps with files
	if len(ps.Steps) > 0 {
		b.WriteString("STEPS:\n")
		for _, step := range ps.Steps {
			status := "[ ]"
			if step.Done {
				status = "[x]"
			}
			b.WriteString(fmt.Sprintf("%s %d. %s\n", status, step.Order, step.Description))
			for _, f := range step.Files {
				b.WriteString(fmt.Sprintf("     - %s\n", f))
			}
		}
		b.WriteString("\n")
	}

	// Acceptance criteria
	if len(ps.AcceptanceCriteria) > 0 {
		b.WriteString("ACCEPTANCE CRITERIA:\n")
		for _, ac := range ps.AcceptanceCriteria {
			b.WriteString(fmt.Sprintf("- %s\n", ac))
		}
		b.WriteString("\n")
	}

	// Test strategy
	if ps.TestStrategy != "" {
		b.WriteString(fmt.Sprintf("TEST STRATEGY: %s\n\n", ps.TestStrategy))
	}

	// Read file contents (max 10 files, max 4KB each)
	seen := make(map[string]bool)
	var files []string
	for _, step := range ps.Steps {
		for _, f := range step.Files {
			if !seen[f] && len(files) < 10 {
				seen[f] = true
				files = append(files, f)
			}
		}
	}

	if len(files) > 0 {
		b.WriteString("FILE CONTENTS:\n")
		for _, f := range files {
			content, err := os.ReadFile(f)
			if err != nil {
				b.WriteString(fmt.Sprintf("--- %s (not found) ---\n\n", f))
				continue
			}
			text := string(content)
			if len(text) > 4096 {
				text = text[:4096] + "\n... (truncated)"
			}
			b.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", f, text))
		}
	}

	return b.String()
}

// srcWorkOrders lists active work orders for the orchestrator.
func srcWorkOrders(p SourceParams) string {
	orders, err := p.D.ListActiveWorkOrders(p.Ctx)
	if err != nil || len(orders) == 0 {
		return "\nWORK ORDERS: inga aktiva\n"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\nWORK ORDERS (%d):\n", len(orders)))
	for _, wo := range orders {
		agent := wo.AgentKey
		if agent == "" {
			agent = "unassigned"
		}
		line := fmt.Sprintf("- %s [%s] agent:%s", wo.Node.Name, wo.Status, agent)
		if wo.BranchName != "" {
			line += fmt.Sprintf(" branch:%s", wo.BranchName)
		}
		if wo.Attempt > 0 {
			line += fmt.Sprintf(" attempt:%d", wo.Attempt)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// srcPipelineStatus aggregates active work orders per status as a health indicator.
func srcPipelineStatus(p SourceParams) string {
	orders, err := p.D.ListActiveWorkOrders(p.Ctx)
	if err != nil || len(orders) == 0 {
		return "\nPIPELINE: idle\n"
	}

	counts := make(map[WorkOrderStatus]int)
	for _, wo := range orders {
		counts[wo.Status]++
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\nPIPELINE STATUS (%d active):\n", len(orders)))
	for _, status := range []WorkOrderStatus{
		WOStatusCreated, WOStatusAssigned, WOStatusMutating,
		WOStatusBuildPassed, WOStatusBuildFailed,
		WOStatusSynthesisPending, WOStatusMergePending,
	} {
		if c, ok := counts[status]; ok {
			b.WriteString(fmt.Sprintf("  %s: %d\n", status, c))
		}
	}
	return b.String()
}

// srcActiveWorkOrder shows the active work order assigned to this agent.
func srcActiveWorkOrder(p SourceParams) string {
	if p.AgentKey == "" {
		return ""
	}
	wo, err := p.D.GetActiveWorkOrderForAgent(p.Ctx, p.AgentKey)
	if err != nil || wo == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n== ACTIVE WORK ORDER ==\n")
	b.WriteString(fmt.Sprintf("Name: %s\n", wo.Node.Name))
	b.WriteString(fmt.Sprintf("Status: %s\n", wo.Status))
	if wo.BranchName != "" {
		b.WriteString(fmt.Sprintf("Branch: %s\n", wo.BranchName))
	}
	if len(wo.ScopePaths) > 0 {
		b.WriteString(fmt.Sprintf("Scope: %s\n", strings.Join(wo.ScopePaths, ", ")))
	}
	if wo.Description != "" {
		b.WriteString(fmt.Sprintf("Task: %s\n", wo.Description))
	}
	b.WriteString("Arbeta inom scope. När du är klar, rapportera till orkestratorn.\n")
	return b.String()
}

// SourceNames returns all registered source names (sorted).
func SourceNames() []string {
	keys := make([]string, 0, len(sourceRegistry))
	for k := range sourceRegistry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
