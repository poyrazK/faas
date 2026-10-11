package state

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsWorkloadServingStore = (*MemStore)(nil)

var _ EnvironmentGitOpsQueueServingAckStore = (*MemStore)(nil)

func (m *MemStore) RecordEnvironmentGitOpsQueueServingAcknowledgement(ctx context.Context,
	acknowledgement EnvironmentWorkloadQueueServingAcknowledgement) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !canonicalStateUUID(acknowledgement.AppID) || !canonicalStateUUID(acknowledgement.BindingID) ||
		(acknowledgement.Mode == "push" && !canonicalStateUUID(acknowledgement.TriggerID)) ||
		(acknowledgement.Mode == "pull" && acknowledgement.TriggerID != "") ||
		(acknowledgement.Mode != "push" && acknowledgement.Mode != "pull") || !canonicalStateUUID(acknowledgement.DeploymentID) ||
		!canonicalStateUUID(acknowledgement.InvocationID) || strings.TrimSpace(acknowledgement.Scope) == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	invocation, exists := m.invocations[acknowledgement.InvocationID]
	if !exists || invocation.State != InvocationCompleted || invocation.Source != InvocationQueue ||
		invocation.AppID != acknowledgement.AppID || invocation.QueueBindingID != acknowledgement.BindingID ||
		invocation.DeploymentScope != acknowledgement.Scope {
		return ErrConflict
	}
	if acknowledgement.Mode == "push" && !m.environmentGitOpsTriggerRecordSucceededLocked(acknowledgement.TriggerID, acknowledgement.InvocationID) {
		return ErrConflict
	}
	return m.recordEnvironmentGitOpsQueueServingAcknowledgementLocked(acknowledgement)
}

func (m *MemStore) environmentGitOpsTriggerRecordSucceededLocked(triggerID, invocationID string) bool {
	for _, record := range m.records {
		if record.TriggerID.String() == triggerID && record.ItemIdentifier == invocationID && record.State == "succeeded" {
			return true
		}
	}
	return false
}

func (m *MemStore) CompleteEnvironmentGitOpsPullQueueDelivery(ctx context.Context, invocationID string, attempt int,
	deploymentID string, result json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !canonicalStateUUID(invocationID) || !canonicalStateUUID(deploymentID) || attempt <= 0 {
		return ErrInvalidArgument
	}
	acknowledgement := &EnvironmentWorkloadQueueServingAcknowledgement{Mode: "pull", DeploymentID: deploymentID,
		InvocationID: invocationID}
	return m.completeInvocationWithQueueServingAck(invocationID, attempt, result, acknowledgement)
}

func (m *MemStore) recordEnvironmentGitOpsQueueServingAcknowledgementLocked(
	acknowledgement EnvironmentWorkloadQueueServingAcknowledgement) error {
	var matchedGraph EnvironmentWorkloadGraph
	var matchedMemory *environmentGitOpsMemory
	var matchedConsumer EnvironmentWorkloadServingQueueConsumer
	var matchedConsumers []EnvironmentWorkloadServingQueueConsumer
	for _, memory := range m.environmentGitOps {
		source := memory.source
		if source.Detached || source.Suspended || source.Spec.Mode != "enforce" || source.EnvironmentSlug != acknowledgement.Scope {
			continue
		}
		for _, graph := range memory.graphs {
			if graph.Phase != "prepared" || graph.Generation != source.Generation || graph.IntentVersion != source.IntentVersion ||
				graph.RevisionID != source.ApprovedRevisionID || graph.SourceID != source.ID {
				continue
			}
			targets := m.environmentGraphActiveReleaseTargetsLocked(source, graph)
			consumers, ok := environmentGraphServingQueueConsumers(graph, targets)
			if !ok {
				continue
			}
			for _, consumer := range consumers {
				if consumer.Mode != acknowledgement.Mode || consumer.AppID != acknowledgement.AppID || consumer.BindingID != acknowledgement.BindingID ||
					consumer.TriggerID != acknowledgement.TriggerID || consumer.DeploymentID != acknowledgement.DeploymentID {
					continue
				}
				if matchedGraph.ID != "" && matchedGraph.ID != graph.ID {
					return ErrConflict
				}
				matchedGraph, matchedConsumer, matchedMemory = graph, consumer, memory
				matchedConsumers = slices.Clone(consumers)
			}
		}
	}
	if matchedGraph.ID == "" || matchedConsumer.BindingID == "" {
		return ErrConflict
	}
	if m.environmentWorkloadQueueServingAcks == nil {
		m.environmentWorkloadQueueServingAcks = map[string]map[string]EnvironmentWorkloadServingQueueAck{}
	}
	if m.environmentWorkloadQueueServingAcks[matchedGraph.ID] == nil {
		m.environmentWorkloadQueueServingAcks[matchedGraph.ID] = map[string]EnvironmentWorkloadServingQueueAck{}
	}
	acks := m.environmentWorkloadQueueServingAcks[matchedGraph.ID]
	if previous, exists := acks[matchedConsumer.BindingID]; exists {
		if previous.Mode != acknowledgement.Mode || previous.AppID != acknowledgement.AppID || previous.TriggerID != acknowledgement.TriggerID || previous.DeploymentID != acknowledgement.DeploymentID {
			return ErrConflict
		}
		return nil
	}
	ack := EnvironmentWorkloadServingQueueAck{Mode: acknowledgement.Mode, AppID: acknowledgement.AppID, BindingID: acknowledgement.BindingID,
		TriggerID: acknowledgement.TriggerID, DeploymentID: acknowledgement.DeploymentID,
		InvocationID: acknowledgement.InvocationID, Acknowledged: time.Now().UTC()}
	acks[matchedConsumer.BindingID] = ack
	if receipt, exists := m.environmentWorkloadServingReceipts[matchedGraph.ID]; exists {
		releaseID := m.activeProjectReleaseSets[releaseKey(matchedMemory.source.ProjectID, matchedMemory.source.EnvironmentSlug)]
		release := m.projectReleaseSets[releaseID]
		receipt.ExpectedQueueConsumers = matchedConsumers
		receipt.QueueAcknowledgements = m.environmentWorkloadQueueServingAcksLocked(matchedGraph.ID)
		receipt.ReleaseCreatedAt = release.CreatedAt
		receipt.ExpectedScheduledJobs = environmentGitOpsScheduledJobMembers(matchedGraph)
		receipt.ScheduledJobAcks = m.environmentWorkloadScheduledJobAcksLocked(matchedGraph, release,
			m.environmentGraphActiveReleaseTargetsLocked(matchedMemory.source, matchedGraph))
		if !receipt.Serving && environmentWorkloadServingReceiptAcknowledged(receipt) {
			now := time.Now().UTC()
			receipt.Serving, receipt.ServedAt = true, &now
		}
		m.environmentWorkloadServingReceipts[matchedGraph.ID] = cloneEnvironmentWorkloadServingReceipt(receipt)
	}
	return nil
}

func (m *MemStore) environmentWorkloadQueueServingAcksLocked(graphID string) []EnvironmentWorkloadServingQueueAck {
	byBinding := m.environmentWorkloadQueueServingAcks[graphID]
	acks := make([]EnvironmentWorkloadServingQueueAck, 0, len(byBinding))
	for _, acknowledgement := range byBinding {
		acks = append(acks, acknowledgement)
	}
	slices.SortFunc(acks, func(a, b EnvironmentWorkloadServingQueueAck) int { return strings.Compare(a.BindingID, b.BindingID) })
	return acks
}

func (m *MemStore) PrepareEnvironmentGitOpsWorkloadServing(ctx context.Context, lease EnvironmentGitOpsLease,
	reviewed environmentsync.Plan, gateways []string) (EnvironmentWorkloadServingReceipt, bool, error) {
	if err := ctx.Err(); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	gateways = canonicalEnvironmentServingGateways(gateways)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, _, err := m.environmentCandidateInputsLocked(lease, reviewed); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	memory := m.environmentGitOps[lease.Source.ID]
	graph, exists := memory.graphs[preparationGraphKey(lease.Source.Generation, reviewed.Hash)]
	if !exists {
		return EnvironmentWorkloadServingReceipt{}, false, ErrNotFound
	}
	evidence := m.environmentWorkloadActivationEvidenceLocked(memory, graph)
	if !evidence.Qualified || !evidence.Activated || !environmentGraphSupportsProductionServing(graph) {
		return EnvironmentWorkloadServingReceipt{}, false, nil
	}
	if !m.environmentGitOpsScheduledJobsReadyLocked(memory, graph, "active") {
		return EnvironmentWorkloadServingReceipt{}, false, nil
	}
	releaseID := m.activeProjectReleaseSets[releaseKey(memory.source.ProjectID, memory.source.EnvironmentSlug)]
	release, exists := m.projectReleaseSets[releaseID]
	if !exists || !release.Active {
		return EnvironmentWorkloadServingReceipt{}, false, ErrConflict
	}
	targets := m.environmentGraphActiveReleaseTargetsLocked(memory.source, graph)
	consumers, ok := environmentGraphServingQueueConsumers(graph, targets)
	if !ok {
		return EnvironmentWorkloadServingReceipt{}, false, nil
	}
	routes := make([]EnvironmentWorkloadServingRoute, 0, len(graph.Members))
	for _, member := range graph.Members {
		deploymentID := targets[member.Resource]
		if deploymentID == "" {
			return EnvironmentWorkloadServingReceipt{}, false, nil
		}
		deployment, ok := m.deployments[deploymentID]
		if !ok || deployment.AppID != member.AppID || deployment.Status != DeployLive || deployment.EnvironmentWorkloadHeld() ||
			normalizedDeploymentScope(deployment.Scope) != normalizedDeploymentScope(memory.source.EnvironmentSlug) {
			return EnvironmentWorkloadServingReceipt{}, false, ErrConflict
		}
		for _, sibling := range m.deployments {
			if member.ExecutionMode != api.ExecutionModeWorker && member.CandidateDeploymentID != "" && sibling.AppID == member.AppID && normalizedDeploymentScope(sibling.Scope) == normalizedDeploymentScope(memory.source.EnvironmentSlug) &&
				sibling.Status == DeployLive && sibling.ID != deploymentID && NormalizeRolloutState(sibling.RolloutState) == "rolling_out" {
				// Do not take over a deployment while the ordinary rollout
				// coordinator still owns its traffic transition.
				return EnvironmentWorkloadServingReceipt{}, false, nil
			}
		}
		if member.ExecutionMode == api.ExecutionModeRequest || member.ExecutionMode == api.ExecutionModeService {
			routes = append(routes, EnvironmentWorkloadServingRoute{AppID: member.AppID, DeploymentID: deploymentID,
				Cutover: member.CandidateDeploymentID != ""})
		}
	}
	scheduledJobs := environmentGitOpsScheduledJobMembers(graph)
	if len(routes) == 0 && len(consumers) == 0 && len(scheduledJobs) == 0 || len(routes) > 0 && len(gateways) == 0 {
		return EnvironmentWorkloadServingReceipt{}, false, nil
	}
	receiptGateways := gateways
	if len(routes) == 0 {
		receiptGateways = []string{}
	}
	slices.SortFunc(routes, func(a, b EnvironmentWorkloadServingRoute) int { return strings.Compare(a.AppID, b.AppID) })
	receipt, exists := m.environmentWorkloadServingReceipts[graph.ID]
	reset := !exists || receipt.ReleaseSetID != release.ID || len(routes) > 0 && !slices.Equal(receipt.ExpectedGateways, receiptGateways) ||
		len(routes) == 0 && len(receipt.ExpectedGateways) != 0 || !sameServingRoutes(receipt.Routes, routes)
	if exists && !m.environmentWorkloadServingCutoverWeightsMatchLocked(memory.source, routes) {
		// A previous gateway acknowledgement proves only the route state that
		// existed at that generation. Start a fresh acknowledgement round after
		// repairing candidate traffic drift.
		reset = true
	}
	if reset {
		for i := range routes {
			m.deploymentRouteGeneration++
			routes[i].Generation = m.deploymentRouteGeneration
		}
		receipt = EnvironmentWorkloadServingReceipt{GraphID: graph.ID, SourceID: graph.SourceID, SourceGeneration: graph.Generation,
			IntentVersion: graph.IntentVersion, RevisionID: graph.RevisionID, PlanHash: graph.PlanHash, ReleaseSetID: release.ID,
			ReleaseCreatedAt: release.CreatedAt, ExpectedGateways: slices.Clone(receiptGateways), Routes: routes,
			Acknowledgements: map[int64][]string{}}
	}
	receipt.ExpectedQueueConsumers = slices.Clone(consumers)
	receipt.QueueAcknowledgements = m.environmentWorkloadQueueServingAcksLocked(graph.ID)
	receipt.ReleaseCreatedAt = release.CreatedAt
	receipt.ExpectedScheduledJobs = scheduledJobs
	receipt.ScheduledJobAcks = m.environmentWorkloadScheduledJobAcksLocked(graph, release, targets)
	// All validations happen before mutation so the memory adapter mirrors the
	// transaction in PgStore: a cutover cannot leave only part of a graph at
	// 100% after a rejected sibling.
	for _, route := range routes {
		if !route.Cutover {
			continue
		}
		for id, sibling := range m.deployments {
			if sibling.AppID != route.AppID || normalizedDeploymentScope(sibling.Scope) != normalizedDeploymentScope(memory.source.EnvironmentSlug) || sibling.Status != DeployLive {
				continue
			}
			if id == route.DeploymentID {
				sibling.TrafficPercent = 100
			} else {
				sibling.TrafficPercent = 0
			}
			sibling.TrafficPercentExplicit = true
			m.deployments[id] = sibling
		}
	}
	if m.environmentWorkloadServingReceipts == nil {
		m.environmentWorkloadServingReceipts = map[string]EnvironmentWorkloadServingReceipt{}
	}
	if !receipt.Serving && environmentWorkloadServingReceiptAcknowledged(receipt) {
		now := time.Now().UTC()
		receipt.Serving = true
		receipt.ServedAt = &now
	}
	m.environmentWorkloadServingReceipts[graph.ID] = cloneEnvironmentWorkloadServingReceipt(receipt)
	return cloneEnvironmentWorkloadServingReceipt(receipt), receipt.Serving, nil
}

func (m *MemStore) environmentWorkloadServingCutoverWeightsMatchLocked(source EnvironmentGitSource,
	routes []EnvironmentWorkloadServingRoute) bool {
	for _, route := range routes {
		if !route.Cutover {
			continue
		}
		target, exists := m.deployments[route.DeploymentID]
		if !exists || target.AppID != route.AppID || target.Status != DeployLive || target.EnvironmentWorkloadHeld() ||
			normalizedDeploymentScope(target.Scope) != normalizedDeploymentScope(source.EnvironmentSlug) || target.TrafficPercent != 100 {
			return false
		}
		for _, sibling := range m.deployments {
			if sibling.AppID == route.AppID && normalizedDeploymentScope(sibling.Scope) == normalizedDeploymentScope(source.EnvironmentSlug) &&
				sibling.Status == DeployLive && sibling.ID != route.DeploymentID && sibling.TrafficPercent != 0 {
				return false
			}
		}
	}
	return true
}

func (m *MemStore) AcknowledgeEnvironmentGitOpsWorkloadServing(ctx context.Context, lease EnvironmentGitOpsLease,
	graphID string, generation int64, node string) (EnvironmentWorkloadServingReceipt, bool, error) {
	if err := ctx.Err(); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	var currentGraph EnvironmentWorkloadGraph
	for _, graph := range memory.graphs {
		if graph.ID == graphID && graph.Generation == lease.Source.Generation && graph.RevisionID == lease.Revision.ID {
			currentGraph = graph
			break
		}
	}
	if currentGraph.ID == "" {
		return EnvironmentWorkloadServingReceipt{}, false, ErrConflict
	}
	receipt, exists := m.environmentWorkloadServingReceipts[graphID]
	if !exists {
		return EnvironmentWorkloadServingReceipt{}, false, ErrNotFound
	}
	if !slices.Contains(receipt.ExpectedGateways, node) {
		return EnvironmentWorkloadServingReceipt{}, false, ErrConflict
	}
	var routeFound bool
	for _, route := range receipt.Routes {
		if route.Generation == generation {
			routeFound = true
			break
		}
	}
	if !routeFound {
		return EnvironmentWorkloadServingReceipt{}, false, ErrConflict
	}
	acked := receipt.Acknowledgements[generation]
	if !slices.Contains(acked, node) {
		acked = append(acked, node)
		slices.Sort(acked)
		receipt.Acknowledgements[generation] = acked
	}
	release := m.projectReleaseSets[receipt.ReleaseSetID]
	receipt.ReleaseCreatedAt = release.CreatedAt
	receipt.ExpectedScheduledJobs = environmentGitOpsScheduledJobMembers(currentGraph)
	receipt.ScheduledJobAcks = m.environmentWorkloadScheduledJobAcksLocked(currentGraph, release,
		m.environmentGraphActiveReleaseTargetsLocked(memory.source, currentGraph))
	if environmentWorkloadServingReceiptAcknowledged(receipt) && !receipt.Serving {
		now := time.Now().UTC()
		receipt.Serving = true
		receipt.ServedAt = &now
	}
	m.environmentWorkloadServingReceipts[graphID] = cloneEnvironmentWorkloadServingReceipt(receipt)
	return cloneEnvironmentWorkloadServingReceipt(receipt), receipt.Serving, nil
}

func (m *MemStore) environmentServingGatewayNamesLocked() []string {
	seen := map[string]struct{}{}
	for _, node := range m.computeNodes {
		role := ""
		if node.Role != nil {
			role = strings.TrimSpace(*node.Role)
		}
		if !node.Active || (role != "compute-only" && role != "compute-node") || node.GatewayTargetURL == nil || strings.TrimSpace(*node.GatewayTargetURL) == "" {
			continue
		}
		seen[node.Name] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func canonicalEnvironmentServingGateways(nodes []string) []string {
	canonical := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node = strings.TrimSpace(node); node != "" {
			canonical = append(canonical, node)
		}
	}
	slices.Sort(canonical)
	return slices.Compact(canonical)
}

func sameServingRoutes(a, b []EnvironmentWorkloadServingRoute) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].AppID != b[i].AppID || a[i].DeploymentID != b[i].DeploymentID || a[i].Cutover != b[i].Cutover {
			return false
		}
	}
	return true
}
