package main

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"dash"

	tea "github.com/charmbracelet/bubbletea"
)

// --- Message types ---

type contextMsg struct {
	ws  *dash.WorkingSet
	err error
}

type dashDataMsg struct {
	tasks      []dash.TaskWithDeps
	sessions   []dash.ActivitySummary
	plans      []*dash.PlanState
	services   []serviceStatus
	workOrders []*dash.WorkOrder
	err        error
}

type intelMsg struct {
	proposals []dash.Proposal
	tree      *dash.HierarchyTree
	err       error
}

type serviceStatus struct {
	Name    string
	Running bool
	PID     string
}

type tickMsg time.Time

// --- Fetchers ---

func fetchContext(d *dash.Dash) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ws, err := d.AssembleWorkingSet(ctx)
		return contextMsg{ws: ws, err: err}
	}
}

func fetchDashData(d *dash.Dash) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tasks, _ := d.GetActiveTasksWithDeps(ctx)
		sessions, _ := d.RecentActivity(ctx, 5)
		plans, _ := d.ListActivePlans(ctx)
		services := checkServices()
		workOrders, _ := d.ListActiveWorkOrders(ctx)
		return dashDataMsg{tasks: tasks, sessions: sessions, plans: plans, services: services, workOrders: workOrders}
	}
}

func fetchIntel(d *dash.Dash) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		proposals, _ := d.GenerateProposals(ctx)
		tree, _ := d.GetHierarchyTree(ctx)
		return intelMsg{proposals: proposals, tree: tree}
	}
}

type agentSnapshotMsg struct {
	snapshot *dash.AgentContextSnapshot
	err      error
}

func fetchAgentSnapshot(d *dash.Dash, agentKey, mission string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		snap, err := d.AssembleAgentSnapshot(ctx, agentKey, mission)
		return agentSnapshotMsg{snapshot: snap, err: err}
	}
}

func checkServices() []serviceStatus {
	type svc struct {
		name    string
		process string
	}
	defs := []svc{
		{"dashwatch", "dashwatch"},
		{"dashmcp", "dashmcp"},
		{"cockpit", "cockpit"},
	}

	var result []serviceStatus
	for _, s := range defs {
		ss := serviceStatus{Name: s.name}
		out, err := exec.Command("pgrep", "-f", s.process).Output()
		if err == nil && len(out) > 0 {
			ss.Running = true
			ss.PID = strings.TrimSpace(strings.Split(string(out), "\n")[0])
		}
		result = append(result, ss)
	}
	return result
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}
