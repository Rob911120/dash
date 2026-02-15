package dash

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

// EvolutionMetrics aggregates performance data across work orders.
type EvolutionMetrics struct {
	Period            TimeRange               `json:"period"`
	WOCreated         int                     `json:"wo_created"`
	WOMerged          int                     `json:"wo_merged"`
	WORejected        int                     `json:"wo_rejected"`
	BuildSuccessRate  float64                 `json:"build_success_rate"`
	SynthesisAvgScore float64                 `json:"synthesis_avg_score"`
	MeanTimeToMerge   time.Duration           `json:"mean_time_to_merge"`
	Steps             StepDurations           `json:"steps"`
	Agents            map[string]AgentMetrics `json:"agents,omitempty"`
}

// StepDurations holds average time spent in each pipeline step.
type StepDurations struct {
	Mutating     time.Duration `json:"mutating"`
	BuildGate    time.Duration `json:"build_gate"`
	Synthesis    time.Duration `json:"synthesis"`
	MergePending time.Duration `json:"merge_pending"`
}

// AgentMetrics holds per-agent performance data.
type AgentMetrics struct {
	WOCount       int     `json:"wo_count"`
	MergedCount   int     `json:"merged_count"`
	RejectedCount int     `json:"rejected_count"`
	AvgScore      float64 `json:"avg_score,omitempty"`
}

// woEventData is the parsed JSON shape of a work_order_event observation's Data field.
type woEventData struct {
	Status   string `json:"status"`
	Actor    string `json:"actor"`
	Detail   string `json:"detail"`
	Revision int    `json:"revision"`
	Attempt  int    `json:"attempt"`
	EventNum int    `json:"event_num"`
	Branch   string `json:"branch"`
	AgentKey string `json:"agent_key"`
}

// parseWOEventData extracts woEventData from a raw JSON observation Data field.
func parseWOEventData(raw json.RawMessage) (woEventData, error) {
	var evt woEventData
	if err := json.Unmarshal(raw, &evt); err != nil {
		return evt, err
	}
	return evt, nil
}

// timestampedEvent pairs a parsed event with the observation timestamp.
type timestampedEvent struct {
	Event woEventData
	At    time.Time
}

// groupEventsByNode groups observations by NodeID and parses their data.
// Returns a map from node ID to chronologically-sorted events (oldest first).
func groupEventsByNode(observations []*Observation) (map[uuid.UUID][]timestampedEvent, error) {
	grouped := make(map[uuid.UUID][]timestampedEvent)
	for _, obs := range observations {
		evt, err := parseWOEventData(obs.Data)
		if err != nil {
			continue // skip unparseable events
		}
		grouped[obs.NodeID] = append(grouped[obs.NodeID], timestampedEvent{
			Event: evt,
			At:    obs.ObservedAt,
		})
	}
	// Sort each group chronologically (oldest first).
	for id := range grouped {
		events := grouped[id]
		sort.Slice(events, func(i, j int) bool {
			return events[i].At.Before(events[j].At)
		})
		grouped[id] = events
	}
	return grouped, nil
}

// computeStepDurations calculates the average duration of each pipeline step
// across all work orders. It looks for transitions between known status pairs.
func computeStepDurations(grouped map[uuid.UUID][]timestampedEvent) StepDurations {
	type accumulator struct {
		total time.Duration
		count int
	}
	accum := map[string]*accumulator{
		"mutating":      {},
		"build_gate":    {},
		"synthesis":     {},
		"merge_pending": {},
	}

	for _, events := range grouped {
		// Build a map from status to first-seen timestamp for this work order.
		statusTimes := make(map[string]time.Time)
		for _, te := range events {
			if _, seen := statusTimes[te.Event.Status]; !seen {
				statusTimes[te.Event.Status] = te.At
			}
		}

		// mutating: assigned -> (build_passed | build_failed)
		if t0, ok := statusTimes[string(WOStatusAssigned)]; ok {
			if t1, ok := statusTimes[string(WOStatusBuildPassed)]; ok {
				accum["mutating"].total += t1.Sub(t0)
				accum["mutating"].count++
			} else if t1, ok := statusTimes[string(WOStatusBuildFailed)]; ok {
				accum["mutating"].total += t1.Sub(t0)
				accum["mutating"].count++
			}
		}

		// build_gate: mutating -> (build_passed | build_failed)
		if t0, ok := statusTimes[string(WOStatusMutating)]; ok {
			if t1, ok := statusTimes[string(WOStatusBuildPassed)]; ok {
				accum["build_gate"].total += t1.Sub(t0)
				accum["build_gate"].count++
			} else if t1, ok := statusTimes[string(WOStatusBuildFailed)]; ok {
				accum["build_gate"].total += t1.Sub(t0)
				accum["build_gate"].count++
			}
		}

		// synthesis: build_passed -> synthesis_pending -> merge_pending
		if t0, ok := statusTimes[string(WOStatusBuildPassed)]; ok {
			if t1, ok := statusTimes[string(WOStatusMergePending)]; ok {
				accum["synthesis"].total += t1.Sub(t0)
				accum["synthesis"].count++
			}
		}

		// merge_pending: merge_pending -> merged
		if t0, ok := statusTimes[string(WOStatusMergePending)]; ok {
			if t1, ok := statusTimes[string(WOStatusMerged)]; ok {
				accum["merge_pending"].total += t1.Sub(t0)
				accum["merge_pending"].count++
			}
		}
	}

	var sd StepDurations
	if accum["mutating"].count > 0 {
		sd.Mutating = accum["mutating"].total / time.Duration(accum["mutating"].count)
	}
	if accum["build_gate"].count > 0 {
		sd.BuildGate = accum["build_gate"].total / time.Duration(accum["build_gate"].count)
	}
	if accum["synthesis"].count > 0 {
		sd.Synthesis = accum["synthesis"].total / time.Duration(accum["synthesis"].count)
	}
	if accum["merge_pending"].count > 0 {
		sd.MergePending = accum["merge_pending"].total / time.Duration(accum["merge_pending"].count)
	}
	return sd
}

// ComputeEvolutionMetrics calculates metrics from work_order_event observations
// within the given time range.
func (d *Dash) ComputeEvolutionMetrics(ctx context.Context, period TimeRange) (*EvolutionMetrics, error) {
	observations, err := d.ListObservationsByType(ctx, "work_order_event", period, 1000)
	if err != nil {
		return nil, err
	}

	grouped, err := groupEventsByNode(observations)
	if err != nil {
		return nil, err
	}

	m := &EvolutionMetrics{
		Period: period,
		Agents: make(map[string]AgentMetrics),
	}

	var buildPassed, buildFailed int
	var mergeTimesTotal time.Duration
	var mergeTimesCount int

	for _, events := range grouped {
		if len(events) == 0 {
			continue
		}

		// Determine the agent for this work order from the first event with an agent_key.
		var agentKey string
		statusTimes := make(map[string]time.Time)
		hasStatus := make(map[string]bool)

		for _, te := range events {
			if te.Event.AgentKey != "" && agentKey == "" {
				agentKey = te.Event.AgentKey
			}
			if !hasStatus[te.Event.Status] {
				statusTimes[te.Event.Status] = te.At
				hasStatus[te.Event.Status] = true
			}
		}

		// Count statuses.
		if hasStatus[string(WOStatusCreated)] {
			m.WOCreated++
		}
		if hasStatus[string(WOStatusMerged)] {
			m.WOMerged++
		}
		if hasStatus[string(WOStatusRejected)] {
			m.WORejected++
		}

		// Build success / failure.
		if hasStatus[string(WOStatusBuildPassed)] {
			buildPassed++
		}
		if hasStatus[string(WOStatusBuildFailed)] {
			buildFailed++
		}

		// Time to merge: created -> merged.
		if hasStatus[string(WOStatusCreated)] && hasStatus[string(WOStatusMerged)] {
			ttm := statusTimes[string(WOStatusMerged)].Sub(statusTimes[string(WOStatusCreated)])
			mergeTimesTotal += ttm
			mergeTimesCount++
		}

		// Per-agent accumulation.
		if agentKey != "" {
			am := m.Agents[agentKey]
			am.WOCount++
			if hasStatus[string(WOStatusMerged)] {
				am.MergedCount++
			}
			if hasStatus[string(WOStatusRejected)] {
				am.RejectedCount++
			}
			m.Agents[agentKey] = am
		}
	}

	// Build success rate.
	buildTotal := buildPassed + buildFailed
	if buildTotal > 0 {
		m.BuildSuccessRate = float64(buildPassed) / float64(buildTotal)
	}

	// Mean time to merge.
	if mergeTimesCount > 0 {
		m.MeanTimeToMerge = mergeTimesTotal / time.Duration(mergeTimesCount)
	}

	// Step durations.
	m.Steps = computeStepDurations(grouped)

	return m, nil
}
