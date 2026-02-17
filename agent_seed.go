package dash

import (
	"context"
	"encoding/json"
	"sort"
)

// AgentDef describes a registered agent in the graph.
type AgentDef struct {
	Key         string `json:"agent_key"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Favorite    bool   `json:"favorite"`
	Mission     string `json:"mission"`
}

// defaultAgents is the seed list of agents.
var defaultAgents = []AgentDef{
	{
		Key: "orchestrator", DisplayName: "🎯 Orchestrator", Description: "Pipeline manager", Favorite: true,
		Mission: `Du är ORKESTRATORN — den centrala pipeline-managern i Dash.
Ditt ansvar:
- Utvärdera uppgifter och skapa WorkOrders
- Tilldela till rätt sub-agent
- Övervaka progress, köra build gate och synthesis
- Besluta merge/reject
Använd work_order, build_gate, pipeline och spawn_agent verktygen.`,
	},
	{
		Key: "cockpit-backend", DisplayName: "🖥️ Backend", Description: "Go/PostgreSQL", Favorite: true,
		Mission: `Du är en backend-specialist för Dash cockpit.
Fokusera på:
- Go-kod och arkitektur i /dash/cmd/cockpit
- Integration med Dash core APIs
- Performance och stabilitet
- Databasinteraktioner`,
	},
	{
		Key: "cockpit-frontend", DisplayName: "🎨 Frontend", Description: "TypeScript/React", Favorite: true,
		Mission: `Du är en frontend-specialist för Dash cockpit TUI.
Fokusera på:
- Bubble Tea komponenter och rendering
- Användarupplevelse och interaktivitet
- Tangentbordsnavigering
- Visuell feedback och animationer`,
	},
	{
		Key: "systemprompt-agent", DisplayName: "📝 Prompts", Description: "Prompt engineering", Favorite: true,
		Mission: `Du är en prompt engineering specialist.
Fokusera på:
- Optimera system prompts för olika agenter
- Skapa tydliga och effektiva instruktioner
- Anpassa prompts för specifika uppgifter
- Testa och iterera på prompt-förbättringar`,
	},
	{
		Key: "database-agent", DisplayName: "🗄️ DB", Description: "Database ops", Favorite: true,
		Mission: `Du är en databasspecialist för Dash.
Fokusera på:
- PostgreSQL schema och migrations
- Query-optimering
- Index-strategier
- Data integrity och constraints`,
	},
	{
		Key: "system-agent", DisplayName: "⚙️ System", Description: "Architecture", Favorite: true,
		Mission: `Du är en systemarkitekt för Dash.
Fokusera på:
- Övergripande systemdesign
- API-kontrakt och interfaces
- Modularitet och separation of concerns
- Performance och skalbarhet`,
	},
	{
		Key: "shift-agent", DisplayName: "🔄 Shift", Description: "Handoff", Favorite: true,
		Mission: `Du är en handoff-specialist.
Din uppgift är att:
- Sammanfatta pågående arbete
- Identifiera nästa steg
- Förbereda kontext för nästa agent/session
- Dokumentera viktiga beslut och insikter`,
	},
	{
		Key: "planner-agent", DisplayName: "📋 Planner", Description: "Planning & review", Favorite: true,
		Mission: `Du är PLANNER-agenten — ansvarig för att ta emot planeringsförfrågningar och skapa strukturerade planer.
Ditt ansvar:
- Ta emot plan-requests (kolla plan(op="list") för aktiva)
- Skapa planer med plan(op="create", ...)
- Granska planer med plan_review
- Vid godkänd plan: skapa work_order(action="create", ...) för exekvering
Använd plan, plan_review och work_order verktygen.`,
	},
}

// EnsureDefaultAgents upserts the default agent nodes into the graph.
func EnsureDefaultAgents(ctx context.Context, d *Dash) error {
	for _, def := range defaultAgents {
		data := map[string]any{
			"agent_key":    def.Key,
			"display_name": def.DisplayName,
			"description":  def.Description,
			"favorite":     def.Favorite,
			"mission":      def.Mission,
		}
		node, err := d.GetOrCreateNode(ctx, LayerAutomation, "agent", def.Key, data)
		if err != nil {
			return err
		}
		// Always update to ensure mission/favorite are current
		if err := d.UpdateNodeData(ctx, node, data); err != nil {
			return err
		}
	}
	return nil
}

// LoadAgentDefs reads all non-deleted agent nodes from the graph.
func LoadAgentDefs(ctx context.Context, d *Dash) []AgentDef {
	nodes, err := d.ListNodesByLayerType(ctx, LayerAutomation, "agent")
	if err != nil {
		return fallbackAgentDefs()
	}

	var defs []AgentDef
	for _, n := range nodes {
		var data map[string]any
		if err := json.Unmarshal(n.Data, &data); err != nil {
			continue
		}
		def := AgentDef{
			Key:         agentStrVal(data, "agent_key", n.Name),
			DisplayName: agentStrVal(data, "display_name", n.Name),
			Description: agentStrVal(data, "description", ""),
			Favorite:    agentBoolVal(data, "favorite"),
			Mission:     agentStrVal(data, "mission", ""),
		}
		if def.Key == "" {
			def.Key = n.Name
		}
		defs = append(defs, def)
	}

	if len(defs) == 0 {
		return fallbackAgentDefs()
	}

	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Key < defs[j].Key
	})
	return defs
}

// fallbackAgentDefs returns the hardcoded defaults when DB is unavailable.
func fallbackAgentDefs() []AgentDef {
	out := make([]AgentDef, len(defaultAgents))
	copy(out, defaultAgents)
	return out
}

func agentStrVal(data map[string]any, key, fallback string) string {
	if v, ok := data[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

func agentBoolVal(data map[string]any, key string) bool {
	if v, ok := data[key].(bool); ok {
		return v
	}
	return false
}
