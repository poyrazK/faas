package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	debugRunningEventKind        = "debug.running_reason"
	debugRunningSchemaVersion    = 1
	runningReasonMinEmitInterval = 15 * time.Second
)

// runningReasonObservation is the scheduler-side representation of one
// point-in-time explanation. It deliberately stays separate from the public
// DTO so the scheduler can retain time.Time values until serialization.
type runningReasonObservation struct {
	AppID                  string
	AccountID              string
	ObservedAt             time.Time
	RunningInstances       int
	ConfiguredMinInstances int
	EffectiveMinInstances  int
	PrewarmMinInstances    int
	IdleTimeoutSeconds     int
	Degraded               bool
	Causes                 []runningReasonCause
}

type runningReasonCause struct {
	Code            string
	Summary         string
	InstanceCount   int
	OpenConnections int64
	TailTasks       int
	Mode            string
	WorkloadClass   string
	LastActivityAt  time.Time
	IdleDeadline    time.Time
}

// runningReasonEvent is the durable JSON shape appended to events with the
// app as subject. AccountID is retained in the payload for right-to-erasure
// cleanup and for operators inspecting raw event rows.
type runningReasonEvent struct {
	SchemaVersion          int                     `json:"schema_version"`
	AppID                  string                  `json:"app_id"`
	AccountID              string                  `json:"account_id,omitempty"`
	ObservedAt             string                  `json:"observed_at"`
	RunningInstances       int                     `json:"running_instances"`
	ConfiguredMinInstances int                     `json:"configured_min_instances"`
	EffectiveMinInstances  int                     `json:"effective_min_instances"`
	PrewarmMinInstances    int                     `json:"prewarm_min_instances,omitempty"`
	IdleTimeoutSeconds     int                     `json:"idle_timeout_seconds"`
	Degraded               bool                    `json:"degraded,omitempty"`
	Causes                 []api.DebugRunningCause `json:"causes"`
}

// runningReasonState prevents the 10-second scheduler tick from turning a
// busy application into an event stream. A changed fingerprint is retained as
// pending and emitted at most once per interval with the latest observation.
type runningReasonState struct {
	emittedFingerprint string
	pendingFingerprint string
	pending            runningReasonEvent
	lastEmit           time.Time
}

// explainRunning evaluates the same gates used by ReapIdle and
// ReapAggressive, but returns their customer-readable causes instead of
// instance IDs to park. The function is pure and therefore easy to exercise
// without a database or clock side effects.
func explainRunning(now time.Time, instances []InstanceInfo) map[string]runningReasonObservation {
	type group struct {
		appID              string
		configuredFloor    int
		effectiveFloor     int
		prewarmFloor       int
		idleTimeoutSeconds int
		running            int
		degraded           bool
		openInstances      int
		openConnections    int64
		tailInstances      int
		tailTasks          int
		recentInstances    int
		startupInstances   int
		unknownInstances   int
		staleCandidates    int
		lastActivity       time.Time
		idleDeadline       time.Time
		cooldownUntil      time.Time
		workloadModes      map[string]int
		workloadClasses    map[string]int
	}
	groups := map[string]*group{}
	for _, in := range instances {
		if in.State != state.StateRunning {
			continue
		}
		g := groups[in.AppID]
		if g == nil {
			g = &group{
				appID:              in.AppID,
				configuredFloor:    in.ConfiguredMinInstances,
				effectiveFloor:     in.MinInstances,
				prewarmFloor:       in.PrewarmMinInstances,
				idleTimeoutSeconds: EffectiveIdleTimeoutS(in.Plan, in.IdleTimeoutS),
				workloadModes:      map[string]int{},
				workloadClasses:    map[string]int{},
			}
			groups[in.AppID] = g
		}
		if in.FlowCountDegraded {
			g.degraded = true
		}
		g.running++
		if in.ConfiguredMinInstances > g.configuredFloor {
			g.configuredFloor = in.ConfiguredMinInstances
		}
		if in.MinInstances > g.effectiveFloor {
			g.effectiveFloor = in.MinInstances
		}
		if in.PrewarmMinInstances > g.prewarmFloor {
			g.prewarmFloor = in.PrewarmMinInstances
		}

		mode := string(state.InstanceMode(in.Mode))
		if mode == "" {
			mode = "normal"
		}
		workload := string(in.WorkloadClass)
		if workload != "" {
			g.workloadClasses[workload]++
		}
		if in.WorkloadClass == state.WorkloadClassWorker || mode == string(state.InstanceModeWorker) || mode == string(state.InstanceModeService) || mode == string(state.InstanceModeJob) || mode == string(state.InstanceModeMirror) {
			g.workloadModes[mode]++
		}
		if in.LastScaleInAt != nil && in.ScaleInCooldownS > 0 {
			until := in.LastScaleInAt.Add(time.Duration(in.ScaleInCooldownS) * time.Second)
			if until.After(now) && until.After(g.cooldownUntil) {
				g.cooldownUntil = until
			}
		}

		if in.OpenConns > 0 {
			g.openInstances++
			g.openConnections += in.OpenConns
		}
		if in.TailCount > 0 {
			g.tailInstances++
			g.tailTasks += in.TailCount
		}
		ref := idleReference(in)
		if ref.IsZero() {
			g.unknownInstances++
			continue
		}
		deadline := ref.Add(time.Duration(g.idleTimeoutSeconds) * time.Second)
		if !in.LastRequest.IsZero() && !now.After(deadline) {
			g.recentInstances++
			if ref.After(g.lastActivity) {
				g.lastActivity = ref
				g.idleDeadline = deadline
			}
		} else if in.LastRequest.IsZero() && !now.After(deadline) {
			g.startupInstances++
		} else if in.OpenConns == 0 && in.TailCount == 0 && !now.Before(deadline) {
			g.staleCandidates++
		}
	}

	out := make(map[string]runningReasonObservation, len(groups))
	for appID, g := range groups {
		causes := make([]runningReasonCause, 0, 6)
		if !g.cooldownUntil.IsZero() {
			causes = append(causes, runningReasonCause{
				Code:          api.DebugRunningReasonScaleInCooldown,
				Summary:       fmt.Sprintf("Scale-in cooldown is active until %s.", g.cooldownUntil.UTC().Format(time.RFC3339Nano)),
				InstanceCount: g.running,
				IdleDeadline:  g.cooldownUntil,
			})
		}
		if g.openInstances > 0 {
			causes = append(causes, runningReasonCause{
				Code:            api.DebugRunningReasonOpenConnection,
				Summary:         fmt.Sprintf("%d active TCP connection(s) keep the instance warm; protocol is not identified.", g.openConnections),
				InstanceCount:   g.openInstances,
				OpenConnections: g.openConnections,
			})
		}
		if g.tailInstances > 0 {
			causes = append(causes, runningReasonCause{
				Code:          api.DebugRunningReasonTailTasks,
				Summary:       fmt.Sprintf("%d active waitUntil task(s) are still draining.", g.tailTasks),
				InstanceCount: g.tailInstances,
				TailTasks:     g.tailTasks,
			})
		}
		if g.recentInstances > 0 {
			causes = append(causes, runningReasonCause{
				Code:           api.DebugRunningReasonRequestActivity,
				Summary:        fmt.Sprintf("%d instance(s) saw successful request activity before the idle deadline.", g.recentInstances),
				InstanceCount:  g.recentInstances,
				LastActivityAt: g.lastActivity,
				IdleDeadline:   g.idleDeadline,
			})
		}
		if g.effectiveFloor > 0 {
			allowed := g.running - g.effectiveFloor
			if allowed < 0 {
				allowed = 0
			}
			held := g.staleCandidates - allowed
			if held > 0 {
				causes = append(causes, runningReasonCause{
					Code:          api.DebugRunningReasonMinInstances,
					Summary:       fmt.Sprintf("An effective minimum of %d warm instance(s) is keeping %d idle instance(s) resident.", g.effectiveFloor, held),
					InstanceCount: held,
				})
				if g.prewarmFloor > g.configuredFloor && g.prewarmFloor == g.effectiveFloor {
					causes = append(causes, runningReasonCause{
						Code:          api.DebugRunningReasonPrewarmFloor,
						Summary:       fmt.Sprintf("A temporary prewarm floor of %d instance(s) is active.", g.prewarmFloor),
						InstanceCount: held,
					})
				}
			}
		}
		if len(g.workloadModes) > 0 {
			mode := firstMapKey(g.workloadModes)
			workload := firstMapKey(g.workloadClasses)
			causes = append(causes, runningReasonCause{
				Code:          api.DebugRunningReasonWorkloadMode,
				Summary:       fmt.Sprintf("%s workload mode is exempt from request-idle parking.", mode),
				InstanceCount: lenForMap(g.workloadModes),
				Mode:          mode,
				WorkloadClass: workload,
			})
		}
		if g.startupInstances > 0 {
			causes = append(causes, runningReasonCause{
				Code:          api.DebugRunningReasonStartupGrace,
				Summary:       fmt.Sprintf("%d instance(s) started recently and have not reached their idle deadline.", g.startupInstances),
				InstanceCount: g.startupInstances,
			})
		}
		if g.unknownInstances > 0 {
			causes = append(causes, runningReasonCause{
				Code:          api.DebugRunningReasonUnknownActivity,
				Summary:       fmt.Sprintf("Activity timestamps are missing for %d instance(s); the scheduler is retaining them conservatively.", g.unknownInstances),
				InstanceCount: g.unknownInstances,
			})
		}
		if len(causes) == 0 {
			causes = append(causes, runningReasonCause{
				Code:          api.DebugRunningReasonNoBlockerObserved,
				Summary:       "No parking blocker was observed in this scheduler tick.",
				InstanceCount: g.running,
			})
		}
		sort.SliceStable(causes, func(i, j int) bool {
			return debugRunningReasonRank(causes[i].Code) < debugRunningReasonRank(causes[j].Code)
		})
		out[appID] = runningReasonObservation{
			AppID:                  appID,
			RunningInstances:       g.running,
			ConfiguredMinInstances: g.configuredFloor,
			EffectiveMinInstances:  g.effectiveFloor,
			PrewarmMinInstances:    g.prewarmFloor,
			IdleTimeoutSeconds:     g.idleTimeoutSeconds,
			Degraded:               g.degraded,
			Causes:                 causes,
		}
	}
	return out
}

func firstMapKey(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func lenForMap(m map[string]int) int {
	total := 0
	for _, n := range m {
		total += n
	}
	return total
}

func debugRunningReasonRank(code string) int {
	for i, value := range api.AllDebugRunningReasonCodes {
		if code == value {
			return i
		}
	}
	return len(api.AllDebugRunningReasonCodes) + 1
}

func runningCauseToAPI(c runningReasonCause) api.DebugRunningCause {
	out := api.DebugRunningCause{
		Code:            c.Code,
		Summary:         c.Summary,
		InstanceCount:   c.InstanceCount,
		OpenConnections: c.OpenConnections,
		TailTasks:       c.TailTasks,
		Mode:            c.Mode,
		WorkloadClass:   c.WorkloadClass,
	}
	if !c.LastActivityAt.IsZero() {
		out.LastActivityAt = c.LastActivityAt.UTC().Format(time.RFC3339Nano)
	}
	if !c.IdleDeadline.IsZero() {
		out.IdleDeadline = c.IdleDeadline.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func runningObservationEvent(obs runningReasonObservation, observedAt time.Time, accountID string) runningReasonEvent {
	causes := make([]api.DebugRunningCause, 0, len(obs.Causes))
	for _, cause := range obs.Causes {
		causes = append(causes, runningCauseToAPI(cause))
	}
	return runningReasonEvent{
		SchemaVersion:          debugRunningSchemaVersion,
		AppID:                  obs.AppID,
		AccountID:              accountID,
		ObservedAt:             observedAt.UTC().Format(time.RFC3339Nano),
		RunningInstances:       obs.RunningInstances,
		ConfiguredMinInstances: obs.ConfiguredMinInstances,
		EffectiveMinInstances:  obs.EffectiveMinInstances,
		PrewarmMinInstances:    obs.PrewarmMinInstances,
		IdleTimeoutSeconds:     obs.IdleTimeoutSeconds,
		Degraded:               obs.Degraded,
		Causes:                 causes,
	}
}

func runningEventFingerprint(event runningReasonEvent) string {
	event.ObservedAt = ""
	event.SchemaVersion = 0
	data, _ := json.Marshal(event)
	return string(data)
}

// recordRunningReasonObservations appends changed explanations to the existing
// events audit stream. The app ID is the subject, which lets the API reuse the
// existing tenant-scoped event index without introducing a high-volume table.
func (l *Loop) recordRunningReasonObservations(ctx context.Context, apps []state.App, snapshot []InstanceInfo, now time.Time) {
	if l == nil || l.engine == nil || l.engine.Store() == nil {
		return
	}
	observations := explainRunning(now, snapshot)
	accounts := make(map[string]string, len(apps))
	for _, app := range apps {
		accounts[app.ID] = app.AccountID
	}
	if l.runningReasonStates == nil {
		l.runningReasonStates = make(map[string]runningReasonState)
	}
	for appID := range l.runningReasonStates {
		if _, ok := observations[appID]; !ok {
			delete(l.runningReasonStates, appID)
		}
	}
	appIDs := make([]string, 0, len(observations))
	for appID := range observations {
		appIDs = append(appIDs, appID)
	}
	sort.Strings(appIDs)
	for _, appID := range appIDs {
		obs := observations[appID]
		event := runningObservationEvent(obs, now, accounts[appID])
		fp := runningEventFingerprint(event)
		state := l.runningReasonStates[appID]
		if fp == state.emittedFingerprint {
			state.pendingFingerprint = ""
			state.pending = runningReasonEvent{}
			l.runningReasonStates[appID] = state
			continue
		}
		if !state.lastEmit.IsZero() && now.Sub(state.lastEmit) < runningReasonMinEmitInterval {
			state.pendingFingerprint = fp
			state.pending = event
			l.runningReasonStates[appID] = state
			continue
		}
		if state.pendingFingerprint != "" && state.pendingFingerprint == fp {
			event = state.pending
		}
		data, err := json.Marshal(event)
		if err != nil {
			if l.log != nil {
				l.log.Warn("reaper: marshal running reason", "app", appID, "err", err)
			}
			continue
		}
		subject := appID
		if err := l.engine.Store().AppendEvent(ctx, "schedd", debugRunningEventKind, &subject, data); err != nil {
			if l.log != nil {
				l.log.Warn("reaper: record running reason", "app", appID, "err", err)
			}
			continue
		}
		state.emittedFingerprint = fp
		state.pendingFingerprint = ""
		state.pending = runningReasonEvent{}
		state.lastEmit = now
		l.runningReasonStates[appID] = state
	}
}
