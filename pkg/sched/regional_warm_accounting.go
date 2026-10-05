package sched

// adr: 627
import (
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// Reserve serving concurrency before an issued intent or RPC may escape. A
// retained reservation can retry at the cap; a different physical attempt cannot.
func (l *NodeLedger) reserveRegionalWarmResume(instance string, p runtimequota.CapacityPermit, max int, recovered bool) error {
	if l == nil || p.Validate() != nil || p.Kind != runtimequota.CapacityWarm || !runtimequota.MatchesID(p.InstanceID, instance) || (!recovered && max <= 0) {
		return runtimequota.ErrBinding
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.entries[instance]
	if r == nil || !runtimequota.MatchesID(p.Grant.Binding.RegionalAppID, r.appID) || !runtimequota.MatchesID(p.DeploymentID, r.deploymentID) || !runtimequota.MatchesID(p.NodeID, r.nodeID) || (r.regionalResumePermitID != "" && r.regionalResumePermitID != p.ID) {
		return runtimequota.ErrBinding
	}
	if r.countsConc {
		if r.regionalResumePermitID == p.ID || recovered {
			r.regionalResumePermitID = p.ID
			return nil
		}
		return runtimequota.ErrBinding
	}
	if !recovered && l.perApp[r.appID] >= max {
		return state.ErrRegionalRuntimeCapacity
	}
	r.regionalResumePermitID = p.ID
	r.countsConc = true
	l.perApp[r.appID]++
	if r.deploymentID != "" {
		l.perAppDeployment[r.appID+"\x00"+r.deploymentID]++
	}
	return nil
}

func (l *NodeLedger) regionalWarmResumePending(instance string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.entries[instance]
	return r != nil && r.countsConc && r.regionalResumePermitID != ""
}
