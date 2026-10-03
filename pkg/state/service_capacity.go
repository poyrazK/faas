package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ServiceCapacityError is a retryable admission refusal, never a VM failure.
// The database trigger is also effective for older daemons and direct SQL.
type ServiceCapacityError struct{}

func (*ServiceCapacityError) Error() string {
	return "service admission would consume bare-metal recovery capacity"
}
func (*ServiceCapacityError) Unwrap() error { return ErrNodeCapacity }

// ServiceCapacityProblem recognizes both Store and direct transaction errors.
func ServiceCapacityProblem(err error) *api.Problem {
	var capacity *ServiceCapacityError
	var pgErr *pgconn.PgError
	if !errors.As(err, &capacity) && (!errors.As(err, &pgErr) || pgErr.ConstraintName != "service_capacity_protection") {
		return nil
	}
	return api.NewProblem(http.StatusServiceUnavailable, api.CodeServiceRecoveryCapacity,
		"Service recovery capacity unavailable", "The fleet needs more recovery headroom before accepting this capacity increase.")
}

func decodeServiceCapacity(data []byte, err error) (api.ServiceCapacityProtection, error) {
	if err != nil {
		return api.ServiceCapacityProtection{}, mapErr(err)
	}
	var out api.ServiceCapacityProtection
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("state: decode service capacity: %w", err)
	}
	return out, nil
}

func (s *PgStore) ServiceCapacityProtection(ctx context.Context) (api.ServiceCapacityProtection, error) {
	return decodeServiceCapacity(sqlc.New().ServiceCapacityProtection(ctx, s.pool))
}

func (s *PgStore) SetServiceCapacityProtection(ctx context.Context, enabled bool) (api.ServiceCapacityProtection, error) {
	return decodeServiceCapacity(sqlc.New().SetServiceCapacityProtection(ctx, s.pool, enabled))
}

type capacityResources struct{ Count, RAM, CPU, VCPU int64 }

// ServiceCapacityPlacement is an internal scheduler projection. Database
// admission still serializes the eventual write after this placement hint.
type ServiceCapacityPlacement struct {
	Enabled bool                            `json:"enabled"`
	Nodes   map[string]ServiceCapacitySlots `json:"placement"`
}

type ServiceCapacitySlots struct {
	Slots int64 `json:"slots"`
	Used  int64 `json:"used"`
}

func (s *PgStore) ServiceCapacityPlacement(ctx context.Context) (ServiceCapacityPlacement, error) {
	data, err := sqlc.New().ServiceCapacityPlacement(ctx, s.pool)
	if err != nil {
		return ServiceCapacityPlacement{}, mapErr(err)
	}
	var out ServiceCapacityPlacement
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("state: decode service placement capacity: %w", err)
	}
	return out, nil
}

func (m *MemStore) ServiceCapacityPlacement(_ context.Context) (ServiceCapacityPlacement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.serviceCapacityProtection {
		return ServiceCapacityPlacement{}, nil
	}
	return ServiceCapacityPlacement{Enabled: true, Nodes: m.serviceCapacitySnapshotLocked().Placement}, nil
}

type serviceCapacitySnapshot struct {
	Report       api.ServiceCapacityProtection
	Demands      map[string]capacityResources
	Other        map[string]capacityResources
	Actual       map[string]int64
	Resident     map[string]capacityResources
	ServiceUsage map[string]int64
	Placement    map[string]ServiceCapacitySlots
}

func (m *MemStore) ServiceCapacityProtection(_ context.Context) (api.ServiceCapacityProtection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.serviceCapacitySnapshotLocked().Report, nil
}

func (m *MemStore) SetServiceCapacityProtection(_ context.Context, enabled bool) (api.ServiceCapacityProtection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prior := m.serviceCapacityProtection
	m.serviceCapacityProtection = enabled
	snapshot := m.serviceCapacitySnapshotLocked()
	if enabled && snapshot.Report.State != "protected" {
		m.serviceCapacityProtection = prior
		return api.ServiceCapacityProtection{}, &ServiceCapacityError{}
	}
	return snapshot.Report, nil
}

func capacitySidecarResources(raw json.RawMessage) (ram, cpu int64) {
	var sidecars []api.Sidecar
	_ = json.Unmarshal(raw, &sidecars)
	for _, sc := range sidecars {
		ram += int64(max(0, sc.RamMB))
		cpuQuota := sc.CPUMillicores
		if cpuQuota <= 0 {
			cpuQuota = api.DefaultAppCPUMillicores
		}
		cpu += int64(cpuQuota)
	}
	return ram, cpu
}

func capacityDeploymentCurrent(status DeploymentStatus) bool {
	switch status {
	case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting, DeployLive:
		return true
	default:
		return false
	}
}

func capacityResident(status State) bool {
	return nodeUsageCounts(status) || status == StateSnapshotting || status == StateMigrating
}

// ADR-422 uses uniform slots as a conservative placement certificate. Every
// admitted service replica fits in one slot, and survivors keep their slots;
// no repacking of healthy replicas is assumed after a failure.
func (m *MemStore) serviceCapacitySnapshotLocked() serviceCapacitySnapshot {
	s := serviceCapacitySnapshot{Demands: map[string]capacityResources{}, Other: map[string]capacityResources{}, Actual: map[string]int64{}, Resident: map[string]capacityResources{}, ServiceUsage: map[string]int64{}, Placement: map[string]ServiceCapacitySlots{}}
	r := api.ServiceCapacityProtection{Enabled: m.serviceCapacityProtection, ReplicaRAMMB: 1, ReplicaCPUMillicores: 1, ReplicaVCPU: 1, PlacementsFit: true}
	for _, app := range m.apps {
		if (app.Status != AppActive && app.Status != AppEvictedCold) || app.Manifest.ExecutionMode != api.ExecutionModeService {
			continue
		}
		desired := 1
		if app.Manifest.ServiceReplicas != nil {
			desired = max(0, app.Manifest.ServiceReplicas.Desired)
		}
		scopes := map[string]struct{}{DefaultEnvScope: {}}
		for _, d := range m.deployments {
			if d.AppID == app.ID && capacityDeploymentCurrent(d.Status) {
				scopes[normalizedDeploymentScope(d.Scope)] = struct{}{}
			}
		}
		limits, _ := api.LimitsFor(m.accounts[app.AccountID].Plan)
		vcpu := int64(limits.VCPU)
		if vcpu <= 0 {
			vcpu = 4
		}
		for scope := range scopes {
			var sideRAM, sideCPU int64
			live, pending := 0, false
			for _, d := range m.deployments {
				if d.AppID != app.ID || normalizedDeploymentScope(d.Scope) != scope || !capacityDeploymentCurrent(d.Status) {
					continue
				}
				ram, cpu := capacitySidecarResources(d.Sidecars)
				sideRAM = max(sideRAM, ram)
				sideCPU = max(sideCPU, cpu)
				if d.Status == DeployLive {
					live++
				} else {
					pending = true
				}
			}
			count := desired
			if desired > 0 && live > 0 && (pending || live > 1) {
				count += api.RolloutConcurrencyGrant
			}
			resource := capacityResources{Count: int64(count), RAM: int64(max(app.RAMMB, api.MustLimitsFor(api.PlanFree).RAMMB)+api.PerVMOverheadMB) + sideRAM, CPU: int64(max(app.CPUMillicores, api.DefaultAppCPUMillicores)) + sideCPU, VCPU: vcpu}
			s.Demands[app.ID+":"+scope] = resource
			r.DesiredReplicas += int64(desired)
			r.ReservedReplicas += resource.Count
			if count > 0 {
				r.ReplicaRAMMB = max(r.ReplicaRAMMB, resource.RAM)
				r.ReplicaCPUMillicores = max(r.ReplicaCPUMillicores, resource.CPU)
				r.ReplicaVCPU = max(r.ReplicaVCPU, resource.VCPU)
			}
		}
	}
	for _, ins := range m.instances {
		if !capacityResident(State(ins.State)) {
			continue
		}
		app := m.apps[ins.AppID]
		dep := m.deployments[ins.DeploymentID]
		key := app.ID + ":" + normalizedDeploymentScope(dep.Scope)
		ram, cpu := capacitySidecarResources(dep.Sidecars)
		limits, _ := api.LimitsFor(m.accounts[app.AccountID].Plan)
		vcpu := int64(limits.VCPU)
		if vcpu <= 0 {
			vcpu = 4
		}
		if ins.AppID == "" {
			vcpu = 1
		}
		cost := capacityResources{RAM: int64(ins.RAMMB+api.PerVMOverheadMB) + ram, CPU: int64(max(app.CPUMillicores, api.DefaultAppCPUMillicores)) + cpu, VCPU: vcpu}
		admitted := m.capacityInstanceResources[ins.ID]
		cost.RAM = max(cost.RAM, admitted.RAM)
		cost.CPU = max(cost.CPU, admitted.CPU)
		cost.VCPU = max(cost.VCPU, admitted.VCPU)
		_, declared := s.Demands[key]
		service := declared && State(ins.State) != StateWarm && (ins.Mode == "" || ins.Mode == string(InstanceModeNormal) || ins.Mode == string(InstanceModeService)) && app.Manifest.ExecutionMode == api.ExecutionModeService
		resident := s.Resident[ins.NodeID]
		resident.RAM += cost.RAM
		resident.CPU += cost.CPU
		resident.VCPU += cost.VCPU
		s.Resident[ins.NodeID] = resident
		if service {
			s.Actual[key]++
			s.ServiceUsage[ins.NodeID]++
			r.ReplicaRAMMB = max(r.ReplicaRAMMB, cost.RAM)
			r.ReplicaCPUMillicores = max(r.ReplicaCPUMillicores, cost.CPU)
			r.ReplicaVCPU = max(r.ReplicaVCPU, cost.VCPU)
		} else {
			other := s.Other[ins.NodeID]
			other.RAM += cost.RAM
			other.CPU += cost.CPU
			other.VCPU += cost.VCPU
			s.Other[ins.NodeID] = other
		}
	}
	var largest int64
	for _, n := range m.computeNodes {
		if n.Lifecycle != NodeLifecycleActive || n.AdmissionCeilingMB <= 0 || n.VPCPUs <= 0 || n.VCPUBudget <= 0 || time.Since(n.LastHeartbeatAt) > DefaultHeartbeatStaleness {
			delete(s.Other, n.ID)
			continue
		}
		other := s.Other[n.ID]
		s.Other[n.ID] = other
		slots := max(int64(0), min((int64(n.AdmissionCeilingMB)-other.RAM)/r.ReplicaRAMMB, (int64(n.VPCPUs)*1000*api.CPUOvercommit-other.CPU)/r.ReplicaCPUMillicores, (int64(n.VCPUBudget)-other.VCPU)/r.ReplicaVCPU))
		r.HealthyNodes++
		r.FleetSlots += slots
		largest = max(largest, slots)
		s.Placement[n.ID] = ServiceCapacitySlots{Slots: slots, Used: s.ServiceUsage[n.ID]}
		if s.ServiceUsage[n.ID] > slots || other.RAM > int64(n.AdmissionCeilingMB) || other.CPU > int64(n.VPCPUs)*1000*api.CPUOvercommit || other.VCPU > int64(n.VCPUBudget) {
			r.PlacementsFit = false
		}
	}
	r.FailoverSlots = r.FleetSlots - largest
	excess := false
	for key, count := range s.Actual {
		if count > s.Demands[key].Count {
			excess = true
		}
	}
	switch {
	case !r.Enabled:
		r.State = "disabled"
	case r.HealthyNodes >= api.ServiceCapacityMinimumHosts && r.PlacementsFit && r.ReservedReplicas <= r.FailoverSlots && !excess:
		r.State = "protected"
	case r.PlacementsFit && r.ReservedReplicas <= r.FleetSlots:
		r.State = "degraded"
	default:
		r.State = "needs_hardware"
	}
	s.Report = r
	return s
}

func (m *MemStore) checkServiceCapacityChangeLocked(prior serviceCapacitySnapshot, instanceWrite bool) error {
	if !m.serviceCapacityProtection {
		return nil
	}
	next := m.serviceCapacitySnapshotLocked()
	grew, excess, physicalGrowth, serviceGrowth := false, false, false, false
	for key, value := range next.Demands {
		old := prior.Demands[key]
		if value.Count > 0 && (value.Count > old.Count || value.RAM > old.RAM || value.CPU > old.CPU || value.VCPU > old.VCPU) {
			grew = true
		}
	}
	for key, value := range next.Resident {
		old := prior.Resident[key]
		nodeGrowth := value.RAM > old.RAM || value.CPU > old.CPU || value.VCPU > old.VCPU
		if !nodeGrowth {
			continue
		}
		physicalGrowth = true
		if _, healthy := next.Other[key]; !healthy {
			return &ServiceCapacityError{}
		}
		other, oldOther := next.Other[key], prior.Other[key]
		if other.RAM > oldOther.RAM || other.CPU > oldOther.CPU || other.VCPU > oldOther.VCPU {
			grew = true
		}
	}

	// Instance writes cannot remove service declarations. A role change that
	// grows ordinary usage must therefore preserve recovery headroom. Intent
	// removal may reclassify resident guests and must still allow stops.
	if instanceWrite {
		for node, other := range next.Other {
			old := prior.Other[node]
			if other.RAM > old.RAM || other.CPU > old.CPU || other.VCPU > old.VCPU {
				grew = true
			}
		}
	}
	// Warm and mirror promotion can occupy another service slot without
	// changing physical totals. Count membership on every node so promotion
	// on an ineligible host cannot disappear from the healthy projection.
	for node, count := range next.ServiceUsage {
		if count <= prior.ServiceUsage[node] {
			continue
		}
		serviceGrowth = true
		if _, healthy := next.Placement[node]; !healthy {
			return &ServiceCapacityError{}
		}
	}
	for key, count := range next.Actual {
		if count > prior.Actual[key] {
			if count > next.Demands[key].Count {
				excess = true
			}
		}
	}
	// Historical guest bounds can enlarge the uniform slot when a resident
	// VM becomes a service, even though no new resources were allocated.
	if next.Report.ReplicaRAMMB > prior.Report.ReplicaRAMMB || next.Report.ReplicaCPUMillicores > prior.Report.ReplicaCPUMillicores || next.Report.ReplicaVCPU > prior.Report.ReplicaVCPU {
		grew = true
	}
	if ((grew || excess) && next.Report.State != "protected") || ((physicalGrowth || serviceGrowth) && !next.Report.PlacementsFit) {
		return &ServiceCapacityError{}
	}
	return nil
}

// These proposal checks restore the map before returning; callers commit
// only after a pass, before activity, snapshots or other side effects.
func (m *MemStore) checkServiceCapacityAppLocked(app App) error {
	if !m.serviceCapacityProtection {
		return nil
	}
	prior := m.serviceCapacitySnapshotLocked()
	old, exists := m.apps[app.ID]
	m.apps[app.ID] = app
	err := m.checkServiceCapacityChangeLocked(prior, false)
	if exists {
		m.apps[app.ID] = old
	} else {
		delete(m.apps, app.ID)
	}
	return err
}

func (m *MemStore) checkServiceCapacityInstanceLocked(ins Instance) error {
	if !m.serviceCapacityProtection {
		return nil
	}
	prior := m.serviceCapacitySnapshotLocked()
	old, exists := m.instances[ins.ID]
	m.instances[ins.ID] = ins
	err := m.checkServiceCapacityChangeLocked(prior, true)
	if exists {
		m.instances[ins.ID] = old
	} else {
		delete(m.instances, ins.ID)
	}
	return err
}

func (m *MemStore) checkServiceCapacityDeploymentLocked(dep Deployment) error {
	if !m.serviceCapacityProtection {
		return nil
	}
	prior := m.serviceCapacitySnapshotLocked()
	old, exists := m.deployments[dep.ID]
	m.deployments[dep.ID] = dep
	err := m.checkServiceCapacityChangeLocked(prior, false)
	if exists {
		m.deployments[dep.ID] = old
	} else {
		delete(m.deployments, dep.ID)
	}
	return err
}

// Keep the admitted shape until this instance's resources are released. Plan
// and app updates do not resize an already-running guest immediately.
func (m *MemStore) recordInstanceCapacityLocked(ins Instance) {
	if m.capacityInstanceResources == nil {
		m.capacityInstanceResources = map[string]capacityResources{}
	}
	app := m.apps[ins.AppID]
	dep := m.deployments[ins.DeploymentID]
	ram, cpu := capacitySidecarResources(dep.Sidecars)
	limits, _ := api.LimitsFor(m.accounts[app.AccountID].Plan)
	vcpu := int64(limits.VCPU)
	if vcpu <= 0 {
		vcpu = 4
	}
	if ins.AppID == "" {
		vcpu = 1
	}
	m.capacityInstanceResources[ins.ID] = capacityResources{RAM: int64(ins.RAMMB+api.PerVMOverheadMB) + ram, CPU: int64(max(app.CPUMillicores, api.DefaultAppCPUMillicores)) + cpu, VCPU: vcpu}
}
