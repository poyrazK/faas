package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestScanDetectionTraceMapsToPlanResponse(t *testing.T) {
	workload := toPlanWorkload(reposcan.Workload{
		Name:                      "web",
		ServiceBindingPolicy:      reposcan.ServiceBindingPolicyDeclared,
		ServiceBindingTransport:   reposcan.ServiceBindingTransportHTTPS,
		PreviewServiceCallsPolicy: reposcan.PreviewServiceCallsDeny,
		DetectedBy: reposcan.Detection{
			Detector:   "compose",
			Marker:     "compose.yaml",
			Priority:   80,
			MergedFrom: []string{"procfile"},
		},
	})
	if workload.DetectedBy == nil || workload.DetectedBy.Marker != "compose.yaml" {
		t.Fatalf("detected_by = %+v, want compose.yaml marker", workload.DetectedBy)
	}
	if workload.ServiceBindingPolicy != api.ServiceBindingPolicyDeclared {
		t.Fatalf("service_binding_policy = %q, want declared", workload.ServiceBindingPolicy)
	}
	if workload.ServiceBindingTransport != api.ServiceBindingTransportHTTPS {
		t.Fatalf("service_binding_transport = %q, want https", workload.ServiceBindingTransport)
	}
	if workload.PreviewServiceCallsPolicy != api.PreviewServiceCallsDeny {
		t.Fatalf("preview_service_calls_policy = %q, want deny", workload.PreviewServiceCallsPolicy)
	}

	warnings := toPlanDetectionWarnings([]reposcan.DetectionWarning{{
		Workload: "web",
		Detector: "procfile",
		Marker:   "Procfile",
		Priority: 75,
		Outcome:  "merged",
		Reason:   "compose won identity",
	}})
	if len(warnings) != 1 || warnings[0] != (api.PlanDetectionWarning{
		Workload: "web",
		Detector: "procfile",
		Marker:   "Procfile",
		Priority: 75,
		Outcome:  "merged",
		Reason:   "compose won identity",
	}) {
		t.Fatalf("detection warnings = %+v", warnings)
	}
}

func TestPlanWorkloadNewProjectDefaultsDeclared(t *testing.T) {
	workload := toPlanWorkload(reposcan.Workload{Name: "frontend"})
	if workload.ServiceBindingPolicy != api.ServiceBindingPolicyDeclared {
		t.Fatalf("new workload policy = %q, want declared", workload.ServiceBindingPolicy)
	}
}
