package state

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestAlertRollbackSelectionFailsClosed(t *testing.T) {
	now := time.Now().UTC()
	base := alertRollbackFacts{RuleID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), Enabled: true, Action: AlertActionRollback, Metric: AlertMetricErrorRate, Name: "error rate"}
	base.AppAccountID = base.AccountID
	candidate := alertRollbackDeployment{ID: uuid.NewString(), AppID: base.AppID, Status: DeployLive, Scope: "default", TrafficPercent: 25, CanaryTotalSteps: 4, CanaryStep: 1, RolloutState: "rolling_out", CreatedAt: now}
	predecessor := alertRollbackDeployment{ID: uuid.NewString(), AppID: base.AppID, Status: DeployLive, Scope: "default", TrafficPercent: 75, CreatedAt: now.Add(-time.Minute)}
	for _, scenario := range []string{"exact pair", "multiple canaries", "multiple recipients", "newer recipient", "completed candidate", "account wide", "foreign account", "service", "pre-auth"} {
		t.Run(scenario, func(t *testing.T) {
			f := base
			f.Deployments = []alertRollbackDeployment{candidate, predecessor}
			switch scenario {
			case "multiple canaries":
				d := candidate
				d.ID = uuid.NewString()
				d.Scope = "prod"
				f.Deployments = append(f.Deployments, d)
			case "multiple recipients":
				d := predecessor
				d.ID = uuid.NewString()
				f.Deployments = append(f.Deployments, d)
			case "newer recipient":
				f.Deployments[1].CreatedAt = now.Add(time.Minute)
			case "completed candidate":
				f.Deployments[0].CanaryStep = 4
				f.Deployments[0].RolloutState = "complete"
			case "account wide":
				f.AppID = ""
			case "foreign account":
				f.AppAccountID = uuid.NewString()
			case "service":
				f.Service = true
			case "pre-auth":
				f.Metric = AlertMetricPreAuthTargetThreshold
			}
			fire := uuid.NewString()
			r := captureAlertRollback(strings.ReplaceAll(fire, "-", ""), f, 42, now)
			if r.ID != fire {
				t.Fatalf("noncanonical fire UUID %s", r.ID)
			}
			if scenario == "exact pair" {
				if r.Status != "pending" || r.CandidateDeploymentID != candidate.ID || r.PredecessorDeploymentID != predecessor.ID {
					t.Fatalf("selection %+v", r)
				}
			} else if r.Status != "failed" || r.Code == "" {
				t.Fatalf("unsafe selection %+v", r)
			}
		})
	}
	m := NewMemStore()
	m.captureAlertRollbackLocked(uuid.NewString(), AlertRule{Action: AlertActionRollback, Metric: AlertMetricPreAuthTargetThreshold}, 42, now)
	if len(m.alertRollbacks) != 0 {
		t.Fatal("pre-auth metric enqueued deployment mutation")
	}
}

func TestAlertServiceRollbackSelectionAndCompletionGuards(t *testing.T) {
	now := time.Now().UTC()
	f := alertRollbackFacts{RuleID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(), Enabled: true, Service: true, Action: AlertActionRollback, Metric: AlertMetricErrorRate}
	f.AppAccountID = f.AccountID
	p := alertRollbackDeployment{ID: uuid.NewString(), AppID: f.AppID, Scope: "default", Status: DeployLive, TrafficPercent: 0, CreatedAt: now.Add(-time.Minute)}
	c := alertRollbackDeployment{ID: uuid.NewString(), AppID: f.AppID, Scope: "default", Status: DeployLive, TrafficPercent: 100, RolloutState: "rolling_out", CreatedAt: now, PredecessorID: p.ID}
	for _, scenario := range []string{"retained zero weight", "unrelated zero weight", "missing pin", "unpinned zero weight", "newer predecessor", "other serving row", "multiple services"} {
		t.Run(scenario, func(t *testing.T) {
			facts := f
			facts.Deployments = []alertRollbackDeployment{c, p}
			switch scenario {
			case "unrelated zero weight", "other serving row":
				d := p
				d.ID = uuid.NewString()
				if scenario == "other serving row" {
					d.TrafficPercent = 10
				}
				facts.Deployments = append(facts.Deployments, d)
			case "missing pin":
				facts.Deployments[0].PredecessorID = uuid.NewString()
			case "unpinned zero weight":
				facts.Deployments[0].PredecessorID = ""
			case "newer predecessor":
				facts.Deployments[1].CreatedAt = now.Add(time.Minute)
			case "multiple services":
				d := c
				d.ID, d.Scope = uuid.NewString(), "prod"
				facts.Deployments = append(facts.Deployments, d)
			}
			r := captureAlertRollback(uuid.NewString(), facts, 42, now)
			valid := scenario == "retained zero weight" || scenario == "unrelated zero weight"
			if valid && (r.Status != "pending" || r.PredecessorDeploymentID != p.ID || !r.Service) || !valid && r.Status != "failed" {
				t.Fatalf("selection %+v", r)
			}
		})
	}
	for _, scenario := range []string{"matching completion", "missing acknowledgement", "missing routing audit", "missing gateway", "replacement request"} {
		t.Run(scenario, func(t *testing.T) {
			fire := uuid.NewString()
			r := api.AlertRollback{ID: fire, AppID: f.AppID, Service: true, ServiceRequestID: fire, CandidateDeploymentID: c.ID, PredecessorDeploymentID: p.ID, Scope: "default", Status: "pending"}
			h := ServiceRolloutHandoff{Action: "abort", Phase: "complete", PredecessorDeploymentID: p.ID, AcknowledgedAt: &now, CompletedAt: &now, BindingsCheck: &api.ServiceRolloutBindingGate{RequestID: fire, Action: "abort", DeploymentID: p.ID, Status: "passed", AuditID: "12"}}
			switch scenario {
			case "missing acknowledgement":
				h.AcknowledgedAt = nil
			case "missing routing audit":
				h.BindingsCheck.AuditID = ""
			case "missing gateway":
				h.MissingGateways = []string{"node-a"}
			case "replacement request":
				h.BindingsCheck.RequestID = uuid.NewString()
			}
			got := projectAlertServiceRollback(r, Deployment{AppID: f.AppID, ID: c.ID, Scope: "default", Status: DeploySuperseded, RolloutState: "aborted", ServiceRolloutHandoff: h}, Deployment{ID: p.ID, AppID: f.AppID, Scope: "default", Status: DeployLive, TrafficPercent: 100})
			if scenario == "matching completion" && got.Status != "complete" || scenario != "matching completion" && got.Status != "failed" {
				t.Fatalf("completion guard %+v", got)
			}
		})
	}
}
