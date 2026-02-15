package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PlanStage represents the current stage in the plan pipeline.
type PlanStage string

const (
	StageOutline  PlanStage = "outline"
	StagePlan     PlanStage = "plan"
	StagePrereqs  PlanStage = "prereqs"
	StageReview   PlanStage = "review"
	StageApproved PlanStage = "approved"
)

// PlanState is the parsed representation of a CONTEXT.plan node's data.
type PlanState struct {
	Node  *Node     `json:"node"`
	Stage PlanStage `json:"stage"`

	// Outline fields
	Goal        string   `json:"goal,omitempty"`
	Scope       string   `json:"scope,omitempty"`
	NonGoals    []string `json:"non_goals,omitempty"`
	Assumptions []string `json:"assumptions,omitempty"`
	Risks       []string `json:"risks,omitempty"`

	// Insights from chat/exploration
	Insights []string `json:"insights,omitempty"`

	// Plan fields
	Milestones         []string   `json:"milestones,omitempty"`
	Steps              []PlanStep `json:"steps,omitempty"`
	AcceptanceCriteria []string   `json:"acceptance_criteria,omitempty"`
	TestStrategy       string     `json:"test_strategy,omitempty"`

	// Prereqs fields
	BlockedBy       []string `json:"blocked_by,omitempty"`
	RequiredModules []string `json:"required_modules,omitempty"`
	MissingAPIs     []string `json:"missing_apis,omitempty"`
	Migrations      []string `json:"migrations,omitempty"`

	// Review fields (filled by critic)
	Review *PlanReview `json:"review,omitempty"`

	// Gate fields (filled after review)
	Gate *PlanGate `json:"gate,omitempty"`
}

// PlanStep represents a single step in the plan.
type PlanStep struct {
	Order          int      `json:"order"`
	Description    string   `json:"description"`
	Files          []string `json:"files,omitempty"`
	EstimatedLines int      `json:"estimated_lines,omitempty"`
	Done           bool     `json:"done,omitempty"`
}

// PlanReview is the result of the deterministic critic.
type PlanReview struct {
	Score   int           `json:"score"`
	Verdict string        `json:"verdict"` // "approve" or "revise"
	Checks  []ReviewCheck `json:"checks"`
	Issues  []string      `json:"issues,omitempty"`
}

// ReviewCheck is a single check performed by the critic.
type ReviewCheck struct {
	Name      string `json:"name"`
	Passed    bool   `json:"passed"`
	Deduction int    `json:"deduction,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// PlanGate is the gate decision after review.
type PlanGate struct {
	Decision  string `json:"decision"` // "auto_run" or "user_approve"
	RiskScore int    `json:"risk_score"`
	Reason    string `json:"reason"`
}

// stageOrder defines the valid progression.
var stageOrder = []PlanStage{StageOutline, StagePlan, StagePrereqs, StageReview, StageApproved}

func nextStage(current PlanStage) (PlanStage, bool) {
	for i, s := range stageOrder {
		if s == current && i < len(stageOrder)-1 {
			return stageOrder[i+1], true
		}
	}
	return "", false
}

// parsePlanData extracts PlanState from a CONTEXT.plan node.
func parsePlanData(node *Node) (*PlanState, error) {
	if node == nil {
		return nil, fmt.Errorf("nil node")
	}

	ps := &PlanState{Node: node}

	var data map[string]any
	if err := json.Unmarshal(node.Data, &data); err != nil {
		return nil, fmt.Errorf("invalid plan data: %w", err)
	}

	ps.Stage = PlanStage(stringVal(data, "stage"))
	if ps.Stage == "" {
		ps.Stage = StageOutline
	}

	// Outline
	ps.Goal = stringVal(data, "goal")
	ps.Scope = stringVal(data, "scope")
	ps.NonGoals = stringSlice(data, "non_goals")
	ps.Assumptions = stringSlice(data, "assumptions")
	ps.Risks = extractNames(data, "risks", "description")
	ps.Insights = stringSlice(data, "insights")

	// Plan
	ps.Milestones = extractNames(data, "milestones", "name")
	ps.AcceptanceCriteria = stringSlice(data, "acceptance_criteria")
	ps.TestStrategy = stringVal(data, "test_strategy")

	if stepsRaw, ok := data["steps"].([]any); ok {
		for i, sr := range stepsRaw {
			switch val := sr.(type) {
			case string:
				// AI sometimes returns steps as plain strings
				if val != "" {
					ps.Steps = append(ps.Steps, PlanStep{
						Order:       i + 1,
						Description: val,
					})
				}
			case map[string]any:
				step := PlanStep{
					Order:       i + 1,
					Description: stringVal(val, "description"),
					Files:       stringSlice(val, "files"),
				}
				if el, ok := val["estimated_lines"].(float64); ok {
					step.EstimatedLines = int(el)
				}
				if d, ok := val["done"].(bool); ok {
					step.Done = d
				}
				ps.Steps = append(ps.Steps, step)
			}
		}
	}

	// Prereqs
	ps.BlockedBy = stringSlice(data, "blocked_by")
	ps.RequiredModules = stringSlice(data, "required_modules")
	ps.MissingAPIs = stringSlice(data, "missing_apis")
	ps.Migrations = stringSlice(data, "migrations")

	// Review
	if reviewRaw, ok := data["review"].(map[string]any); ok {
		ps.Review = parseReview(reviewRaw)
	}

	// Gate
	if gateRaw, ok := data["gate"].(map[string]any); ok {
		ps.Gate = &PlanGate{
			Decision:  stringVal(gateRaw, "decision"),
			RiskScore: intVal(gateRaw, "risk_score"),
			Reason:    stringVal(gateRaw, "reason"),
		}
	}

	return ps, nil
}

func parseReview(m map[string]any) *PlanReview {
	r := &PlanReview{
		Score:   intVal(m, "score"),
		Verdict: stringVal(m, "verdict"),
		Issues:  stringSlice(m, "issues"),
	}
	if checksRaw, ok := m["checks"].([]any); ok {
		for _, cr := range checksRaw {
			cm, ok := cr.(map[string]any)
			if !ok {
				continue
			}
			r.Checks = append(r.Checks, ReviewCheck{
				Name:      stringVal(cm, "name"),
				Passed:    boolVal(cm, "passed"),
				Deduction: intVal(cm, "deduction"),
				Detail:    stringVal(cm, "detail"),
			})
		}
	}
	return r
}

// --- Validation per stage ---

func validateOutline(ps *PlanState) error {
	var missing []string
	if ps.Goal == "" {
		missing = append(missing, "goal")
	}
	if ps.Scope == "" {
		missing = append(missing, "scope")
	}
	if len(ps.NonGoals) == 0 {
		missing = append(missing, "non_goals (at least 1)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("outline incomplete: missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func validatePlan(ps *PlanState) error {
	var missing []string
	if len(ps.Milestones) == 0 {
		missing = append(missing, "milestones")
	}
	if len(ps.Steps) == 0 {
		missing = append(missing, "steps")
	}
	if len(ps.AcceptanceCriteria) == 0 {
		missing = append(missing, "acceptance_criteria")
	}
	if ps.TestStrategy == "" {
		missing = append(missing, "test_strategy")
	}
	if len(missing) > 0 {
		return fmt.Errorf("plan incomplete: missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func validatePrereqs(ps *PlanState) error {
	// Prereqs validation: auto-fill missing fields with empty arrays instead of blocking.
	var data map[string]any
	if err := json.Unmarshal(ps.Node.Data, &data); err != nil {
		return fmt.Errorf("invalid data: %w", err)
	}
	changed := false
	for _, field := range []string{"blocked_by", "required_modules", "missing_apis", "migrations"} {
		if _, ok := data[field]; !ok {
			data[field] = []any{}
			changed = true
		}
	}
	if changed {
		dataJSON, _ := json.Marshal(data)
		ps.Node.Data = dataJSON
	}
	return nil
}

// --- Deterministic critic ---

func reviewPlan(ps *PlanState) PlanReview {
	score := 100
	var checks []ReviewCheck
	var issues []string

	// Check: acceptance criteria
	if len(ps.AcceptanceCriteria) == 0 {
		score -= 30
		checks = append(checks, ReviewCheck{Name: "acceptance_criteria", Passed: false, Deduction: 30, Detail: "No acceptance criteria defined"})
		issues = append(issues, "Add acceptance criteria to define when the plan is done")
	} else {
		checks = append(checks, ReviewCheck{Name: "acceptance_criteria", Passed: true, Detail: fmt.Sprintf("%d criteria defined", len(ps.AcceptanceCriteria))})
	}

	// Check: steps without files
	stepsWithoutFiles := 0
	for _, s := range ps.Steps {
		if len(s.Files) == 0 {
			stepsWithoutFiles++
		}
	}
	if stepsWithoutFiles > 0 {
		deduction := stepsWithoutFiles * 5
		if deduction > 25 {
			deduction = 25
		}
		score -= deduction
		checks = append(checks, ReviewCheck{Name: "steps_with_files", Passed: false, Deduction: deduction, Detail: fmt.Sprintf("%d/%d steps have no files", stepsWithoutFiles, len(ps.Steps))})
		issues = append(issues, "Add target files to steps for better traceability")
	} else if len(ps.Steps) > 0 {
		checks = append(checks, ReviewCheck{Name: "steps_with_files", Passed: true, Detail: "All steps have target files"})
	}

	// Check: test strategy
	if ps.TestStrategy == "" {
		score -= 20
		checks = append(checks, ReviewCheck{Name: "test_strategy", Passed: false, Deduction: 20, Detail: "No test strategy"})
		issues = append(issues, "Define a test strategy")
	} else {
		checks = append(checks, ReviewCheck{Name: "test_strategy", Passed: true, Detail: "Test strategy defined"})
	}

	// Check: risks
	if len(ps.Risks) == 0 {
		score -= 10
		checks = append(checks, ReviewCheck{Name: "risks", Passed: false, Deduction: 10, Detail: "No risks identified"})
		issues = append(issues, "Identify potential risks")
	} else {
		checks = append(checks, ReviewCheck{Name: "risks", Passed: true, Detail: fmt.Sprintf("%d risks identified", len(ps.Risks))})
	}

	// Check: scope too broad
	scopeLower := strings.ToLower(ps.Scope)
	if scopeLower == "everything" || scopeLower == "allt" {
		score -= 15
		checks = append(checks, ReviewCheck{Name: "scope_bounded", Passed: false, Deduction: 15, Detail: "Scope is too broad"})
		issues = append(issues, "Narrow the scope")
	} else {
		checks = append(checks, ReviewCheck{Name: "scope_bounded", Passed: true, Detail: "Scope is bounded"})
	}

	// Check: estimated lines
	totalLines := 0
	for _, s := range ps.Steps {
		totalLines += s.EstimatedLines
	}
	if totalLines > 500 {
		score -= 10
		checks = append(checks, ReviewCheck{Name: "size_manageable", Passed: false, Deduction: 10, Detail: fmt.Sprintf("Total estimated lines: %d (>500)", totalLines)})
		issues = append(issues, "Consider splitting into smaller plans")
	} else if totalLines > 0 {
		checks = append(checks, ReviewCheck{Name: "size_manageable", Passed: true, Detail: fmt.Sprintf("Total estimated lines: %d", totalLines)})
	}

	// Check: prereqs present
	var data map[string]any
	json.Unmarshal(ps.Node.Data, &data)
	if _, ok := data["blocked_by"]; !ok {
		score -= 10
		checks = append(checks, ReviewCheck{Name: "prereqs_defined", Passed: false, Deduction: 10, Detail: "Prerequisites not defined"})
		issues = append(issues, "Define prerequisites (even if empty)")
	} else {
		checks = append(checks, ReviewCheck{Name: "prereqs_defined", Passed: true, Detail: "Prerequisites defined"})
	}

	if score < 0 {
		score = 0
	}

	verdict := "approve"
	if score < 60 {
		verdict = "revise"
	}

	return PlanReview{
		Score:   score,
		Verdict: verdict,
		Checks:  checks,
		Issues:  issues,
	}
}

// --- Gate ---

func gatePlan(ps *PlanState, review PlanReview) PlanGate {
	totalLines := 0
	for _, s := range ps.Steps {
		totalLines += s.EstimatedLines
	}

	allChecksPassed := true
	for _, c := range review.Checks {
		if !c.Passed {
			allChecksPassed = false
			break
		}
	}

	riskScore := 100 - review.Score
	if totalLines > 300 {
		riskScore += 20
	}
	if riskScore > 100 {
		riskScore = 100
	}

	if riskScore < 40 && allChecksPassed && totalLines <= 300 {
		return PlanGate{
			Decision:  "auto_run",
			RiskScore: riskScore,
			Reason:    fmt.Sprintf("Low risk (score=%d, lines=%d, all checks pass)", riskScore, totalLines),
		}
	}

	return PlanGate{
		Decision:  "user_approve",
		RiskScore: riskScore,
		Reason:    fmt.Sprintf("Needs review (risk=%d, lines=%d, all_pass=%v)", riskScore, totalLines, allChecksPassed),
	}
}

// --- AI plan generation ---

const planGenerationSystemPrompt = `You are a plan generator for Dash, a self-improving graph system.
Given a conversation and codebase context, produce a structured implementation plan as JSON.

Return ONLY valid JSON (no markdown fences, no explanation):
{
  "name": "kebab-case-name",
  "goal": "concrete goal from the discussion",
  "scope": "what files/modules are affected",
  "non_goals": ["explicit exclusions"],
  "assumptions": ["key assumptions"],
  "risks": [{"description": "risk description"}],
  "milestones": [{"name": "phase name"}],
  "steps": [{"description": "what to do", "files": ["/dash/path/to/file.go"], "estimated_lines": 50}],
  "acceptance_criteria": ["verifiable criteria"],
  "test_strategy": "how to verify the change works",
  "blocked_by": [],
  "required_modules": ["dash/package"],
  "missing_apis": [],
  "migrations": []
}

Rules:
- Use file paths from the context pack when populating steps.files
- Keep steps concrete and actionable
- Each step should touch 1-3 files
- Acceptance criteria must be verifiable
- Name should be kebab-case, max 5 words`

// GeneratePlanFromChat uses AI to generate a complete plan from a chat conversation.
// It assembles a context pack for codebase awareness and runs the conversation + context
// through an LLM to produce a structured plan with milestones, steps, and acceptance criteria.
// Falls back to a simple outline if AI is unavailable.
func (d *Dash) GeneratePlanFromChat(ctx context.Context, messages []ChatMessage, scopeName string) (*Node, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("no messages to generate plan from")
	}

	// Extract search query from the last substantive user message
	query := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && len(messages[i].Content) > 10 {
			query = messages[i].Content
			if len(query) > 200 {
				query = query[:200]
			}
			break
		}
	}
	if query == "" {
		query = scopeName
	}
	if query == "" {
		query = "implementation plan"
	}

	// Try AI generation
	if !d.HasRealSummarizer() {
		return d.fallbackPlan(ctx, messages, scopeName)
	}

	// Assemble context pack for codebase awareness
	pack, err := d.AssembleContextPack(ctx, query, ProfilePlan, nil)
	if err != nil {
		pack = nil // proceed without context pack
	}

	// Build user prompt: context + conversation + scope
	var userPrompt strings.Builder
	if pack != nil {
		userPrompt.WriteString(pack.RenderForPrompt())
		userPrompt.WriteString("\n")
	}
	userPrompt.WriteString("CONVERSATION:\n")
	for _, msg := range messages {
		if msg.Role == "user" || msg.Role == "assistant" {
			role := "User"
			if msg.Role == "assistant" {
				role = "AI"
			}
			content := msg.Content
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			userPrompt.WriteString(fmt.Sprintf("%s: %s\n", role, content))
		}
	}
	if scopeName != "" {
		userPrompt.WriteString(fmt.Sprintf("\nScope: %s\n", scopeName))
	} else {
		userPrompt.WriteString("\nScope: free chat\n")
	}

	// Call AI
	response, err := d.summarizer.Complete(ctx, planGenerationSystemPrompt, userPrompt.String())
	if err != nil {
		return d.fallbackPlan(ctx, messages, scopeName)
	}

	// Strip markdown code fences if present
	response = strings.TrimSpace(response)
	if strings.HasPrefix(response, "```") {
		if idx := strings.Index(response[3:], "\n"); idx >= 0 {
			response = response[3+idx+1:]
		}
		if strings.HasSuffix(response, "```") {
			response = response[:len(response)-3]
		}
		response = strings.TrimSpace(response)
	}

	// Parse JSON response
	var planData map[string]any
	if err := json.Unmarshal([]byte(response), &planData); err != nil {
		return d.fallbackPlan(ctx, messages, scopeName)
	}

	// Validate minimum fields
	name, _ := planData["name"].(string)
	goal, _ := planData["goal"].(string)
	if name == "" || goal == "" {
		return d.fallbackPlan(ctx, messages, scopeName)
	}

	// Ensure required fields exist for stage advancement
	if s, _ := planData["scope"].(string); s == "" {
		if scopeName != "" {
			planData["scope"] = scopeName
		} else {
			planData["scope"] = "Från chatt-analys"
		}
	}
	if ng, _ := planData["non_goals"].([]any); len(ng) == 0 {
		planData["non_goals"] = []any{"Utanför scope"}
	}
	for _, field := range []string{"blocked_by", "required_modules", "missing_apis", "migrations"} {
		if _, ok := planData[field]; !ok {
			planData[field] = []any{}
		}
	}

	// Create plan node
	node, err := d.CreatePlan(ctx, name, planData)
	if err != nil {
		return nil, fmt.Errorf("create plan: %w", err)
	}

	// Auto-advance through stages: outline → plan → prereqs → review
	var advanceErr error
	for _, expectedStage := range []PlanStage{StageOutline, StagePlan, StagePrereqs, StageReview} {
		ps, err := d.AdvancePlan(ctx, node.ID)
		if err != nil {
			advanceErr = fmt.Errorf("advance from %s failed: %w", expectedStage, err)
			break
		}
		node = ps.Node
		// If review sent it back to plan (revise), stop
		if expectedStage == StageReview && ps.Stage == StagePlan {
			break
		}
		if ps.Stage == StageApproved {
			break
		}
	}

	// Store advance error in plan data so TUI can display it
	if advanceErr != nil {
		var data map[string]any
		json.Unmarshal(node.Data, &data)
		data["advance_error"] = advanceErr.Error()
		dataJSON, _ := json.Marshal(data)
		node.Data = dataJSON
		d.UpdateNode(ctx, node)
	}

	// Re-fetch to get final state
	finalNode, err := d.GetNodeActive(ctx, node.ID)
	if err != nil {
		return node, nil
	}
	return finalNode, nil
}

// fallbackPlan creates a simple outline plan when AI generation is unavailable.
func (d *Dash) fallbackPlan(ctx context.Context, messages []ChatMessage, scopeName string) (*Node, error) {
	// Extract the last substantive user message as goal
	goal := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && len(messages[i].Content) > 5 {
			goal = messages[i].Content
			if len(goal) > 300 {
				goal = goal[:300]
			}
			break
		}
	}
	if goal == "" {
		goal = scopeName
	}

	// Generate a clean name: use scopeName or extract significant words
	name := scopeName
	if name == "" {
		// Extract significant words (4+ chars, skip common Swedish/English words)
		skip := map[string]bool{
			"låt": true, "oss": true, "ska": true, "för": true, "att": true,
			"med": true, "den": true, "det": true, "och": true, "som": true,
			"let": true, "the": true, "and": true, "for": true, "with": true,
		}
		var words []string
		for _, w := range strings.Fields(strings.ToLower(goal)) {
			w = strings.Trim(w, ".,;:!?()[]{}\"'")
			if len(w) >= 3 && !skip[w] && len(words) < 4 {
				words = append(words, w)
			}
		}
		if len(words) > 0 {
			name = strings.Join(words, "-")
		} else {
			name = fmt.Sprintf("plan-%d", time.Now().Unix()%10000)
		}
	}

	scope := scopeName
	if scope == "" {
		scope = "Från chatt-analys (AI ej tillgänglig)"
	}

	data := map[string]any{
		"goal":             goal,
		"scope":            scope,
		"non_goals":        []string{"Utanför scope"},
		"advance_error":    "AI-generering misslyckades – planen kräver manuell ifyllning av milestones, steps och acceptance_criteria",
		"blocked_by":       []any{},
		"required_modules": []any{},
		"missing_apis":     []any{},
		"migrations":       []any{},
	}

	return d.CreatePlan(ctx, name, data)
}

// --- CRUD ---

// CreatePlan creates a new CONTEXT.plan node at stage=outline.
func (d *Dash) CreatePlan(ctx context.Context, name string, data map[string]any) (*Node, error) {
	if name == "" {
		return nil, fmt.Errorf("plan name is required")
	}
	if data == nil {
		data = make(map[string]any)
	}
	data["stage"] = string(StageOutline)

	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("invalid data: %w", err)
	}

	node := &Node{
		Layer: LayerContext,
		Type:  "plan",
		Name:  name,
		Data:  dataJSON,
	}

	if err := d.CreateNode(ctx, node); err != nil {
		return nil, err
	}

	// Auto-link to intent
	desc := stringVal(data, "goal") + " " + stringVal(data, "scope")
	d.AutoLinkTaskToIntent(ctx, node.ID, name, desc)

	// Embed async
	go d.EmbedNode(context.Background(), node)

	return node, nil
}

// AdvancePlan validates and moves a plan to the next stage.
func (d *Dash) AdvancePlan(ctx context.Context, planID uuid.UUID) (*PlanState, error) {
	node, err := d.GetNodeActive(ctx, planID)
	if err != nil {
		return nil, err
	}
	if node.Type != "plan" || node.Layer != LayerContext {
		return nil, fmt.Errorf("node %s is not a CONTEXT.plan", planID)
	}

	ps, err := parsePlanData(node)
	if err != nil {
		return nil, err
	}

	switch ps.Stage {
	case StageOutline:
		if err := validateOutline(ps); err != nil {
			return ps, err
		}
		return d.setPlanStage(ctx, ps, StagePlan)

	case StagePlan:
		if err := validatePlan(ps); err != nil {
			return ps, err
		}
		return d.setPlanStage(ctx, ps, StagePrereqs)

	case StagePrereqs:
		if err := validatePrereqs(ps); err != nil {
			return ps, err
		}
		return d.setPlanStage(ctx, ps, StageReview)

	case StageReview:
		// Run critic
		review := reviewPlan(ps)
		ps.Review = &review

		// Save review to node data
		var data map[string]any
		json.Unmarshal(node.Data, &data)
		reviewJSON, _ := json.Marshal(review)
		var reviewMap map[string]any
		json.Unmarshal(reviewJSON, &reviewMap)
		data["review"] = reviewMap

		if review.Verdict == "approve" {
			// Run gate
			gate := gatePlan(ps, review)
			ps.Gate = &gate
			gateJSON, _ := json.Marshal(gate)
			var gateMap map[string]any
			json.Unmarshal(gateJSON, &gateMap)
			data["gate"] = gateMap
			data["stage"] = string(StageApproved)
			ps.Stage = StageApproved
		} else {
			// Revise: send back to plan stage
			data["stage"] = string(StagePlan)
			ps.Stage = StagePlan
		}

		dataJSON, _ := json.Marshal(data)
		node.Data = dataJSON
		ps.Node = node
		if err := d.UpdateNode(ctx, node); err != nil {
			return ps, err
		}
		return ps, nil

	case StageApproved:
		return ps, fmt.Errorf("plan is already approved")

	default:
		return ps, fmt.Errorf("unknown stage: %s", ps.Stage)
	}
}

// ReviewPlan runs the critic on a plan without advancing it.
// If forceVerdict is non-empty, the verdict is overridden.
func (d *Dash) ReviewPlan(ctx context.Context, planID uuid.UUID, forceVerdict string) (*PlanState, error) {
	node, err := d.GetNodeActive(ctx, planID)
	if err != nil {
		return nil, err
	}
	ps, err := parsePlanData(node)
	if err != nil {
		return nil, err
	}
	review := reviewPlan(ps)
	if forceVerdict != "" {
		review.Verdict = forceVerdict
		review.Issues = append(review.Issues, fmt.Sprintf("Verdict overridden to '%s' by user", forceVerdict))
	}
	ps.Review = &review
	gate := gatePlan(ps, review)
	ps.Gate = &gate
	return ps, nil
}

// GetPlan retrieves and parses a plan by ID.
func (d *Dash) GetPlan(ctx context.Context, planID uuid.UUID) (*PlanState, error) {
	node, err := d.GetNodeActive(ctx, planID)
	if err != nil {
		return nil, err
	}
	return parsePlanData(node)
}

// GetPlanByName retrieves a plan by name.
func (d *Dash) GetPlanByName(ctx context.Context, name string) (*PlanState, error) {
	node, err := d.GetNodeByName(ctx, LayerContext, "plan", name)
	if err != nil {
		return nil, err
	}
	return parsePlanData(node)
}

const queryListActivePlans = `
	SELECT id, layer, type, name, data, created_at, updated_at, deleted_at
	FROM nodes
	WHERE layer = 'CONTEXT' AND type = 'plan'
	  AND deleted_at IS NULL
	  AND COALESCE(data->>'stage', 'outline') != 'completed'
	ORDER BY updated_at DESC`

// ListActivePlans returns all active plans (not completed or deleted).
func (d *Dash) ListActivePlans(ctx context.Context) ([]*PlanState, error) {
	rows, err := d.db.QueryContext(ctx, queryListActivePlans)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []*PlanState
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			continue
		}
		ps, err := parsePlanData(node)
		if err != nil {
			continue
		}
		plans = append(plans, ps)
	}
	return plans, rows.Err()
}

// setPlanStage updates stage and persists.
func (d *Dash) setPlanStage(ctx context.Context, ps *PlanState, stage PlanStage) (*PlanState, error) {
	var data map[string]any
	json.Unmarshal(ps.Node.Data, &data)
	data["stage"] = string(stage)

	dataJSON, _ := json.Marshal(data)
	ps.Node.Data = dataJSON
	ps.Stage = stage

	if err := d.UpdateNode(ctx, ps.Node); err != nil {
		return ps, err
	}
	return ps, nil
}

// --- JSON helpers ---

func stringVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// extractNames handles arrays that can be either strings or objects with a named field.
// e.g. milestones: ["foo"] or milestones: [{"name":"foo","done":false}]
func extractNames(m map[string]any, key, field string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		switch val := v.(type) {
		case string:
			out = append(out, val)
		case map[string]any:
			if s, ok := val[field].(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func stringSlice(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func intVal(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

func boolVal(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}
