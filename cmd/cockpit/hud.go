package main

import (
	"fmt"
	"strings"

	"dash"
)

type hudModel struct{}

func newHudModel() hudModel { return hudModel{} }

// View renders the HUD: 1-2 content lines inside a rounded border.
func (h hudModel) View(ws *dash.WorkingSet, services []serviceStatus, streaming bool, width int) string {
	if width < 20 {
		width = 80
	}
	innerW := width - 4 // border + padding

	// Line 1: MISSION | NOW | services
	var line1 strings.Builder

	missionName := "..."
	currentFocus := ""
	blockers := ""

	if ws != nil {
		if ws.Mission != nil {
			missionName = ws.Mission.Name
		}
		if ws.ContextFrame != nil {
			data := nodeData(ws.ContextFrame)
			if cf, ok := data["current_focus"].(string); ok && cf != "" {
				currentFocus = cf
			}
			if b, ok := data["blockers"].(string); ok && b != "" && b != "none" {
				blockers = b
			}
		}
	}

	line1.WriteString(hudLabel.Render("MISSION"))
	line1.WriteString(hudLabel.Render(": "))
	line1.WriteString(hudValue.Render(truncate(missionName, innerW/3)))
	line1.WriteString(hudSep)

	if currentFocus != "" {
		line1.WriteString(hudLabel.Render("NOW"))
		line1.WriteString(hudLabel.Render(": "))
		line1.WriteString(hudValue.Render(truncate(currentFocus, innerW/3)))
		line1.WriteString(hudSep)
	}

	if streaming {
		line1.WriteString(chatStreaming.Render("streaming..."))
		line1.WriteString(hudSep)
	}

	// Service indicators
	for _, s := range services {
		if s.Running {
			line1.WriteString(textSuccess.Render("\u25cf"))
		} else {
			line1.WriteString(textAlert.Render("\u25cb"))
		}
		line1.WriteString(" ")
	}

	// Build content
	var content strings.Builder
	content.WriteString(line1.String())

	if blockers != "" {
		content.WriteString("\n")
		content.WriteString(blockerLine.Render(fmt.Sprintf("\u26a0 BLOCKERS: %s", truncate(blockers, innerW-14))))
	}

	return hudBorder.Width(innerW).Render(content.String())
}

// Height returns the number of terminal lines the HUD occupies.
func (h hudModel) Height(ws *dash.WorkingSet) int {
	hasBlockers := false
	if ws != nil && ws.ContextFrame != nil {
		data := nodeData(ws.ContextFrame)
		if b, ok := data["blockers"].(string); ok && b != "" && b != "none" {
			hasBlockers = true
		}
	}
	if hasBlockers {
		return 4 // top border + content + blockers + bottom border
	}
	return 3 // top border + content + bottom border
}
