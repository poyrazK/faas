package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

func TestAggregatePRPreviewMembers(t *testing.T) {
	root := previewMemberStatus{AppID: "root", Slug: "pr-42-api", PreviewOfSlug: "api", WorkloadName: "api", AppStatus: "active", PreviewState: "open", DeploymentStatus: "live"}
	worker := previewMemberStatus{AppID: "worker", Slug: "pr-42-worker", WorkloadName: "worker", AppStatus: "active", PreviewState: "open", DeploymentStatus: "building"}
	tests := []struct {
		name       string
		members    []previewMemberStatus
		wantPhase  githubdgrpc.CheckPhase
		wantDetail string
	}{
		{name: "root cannot green while sibling builds", members: []previewMemberStatus{root, worker}, wantPhase: githubdgrpc.CheckPhaseBuilding, wantDetail: "1/2 workloads live"},
		{name: "root cannot green before sibling deployment exists", members: []previewMemberStatus{root, {AppID: "worker", WorkloadName: "worker", AppStatus: "active", PreviewState: "open", DeploymentStatus: "missing"}}, wantPhase: githubdgrpc.CheckPhaseBuilding, wantDetail: "waiting for worker"},
		{name: "sibling failure wins over live root", members: []previewMemberStatus{root, {AppID: "worker", WorkloadName: "worker", AppStatus: "active", PreviewState: "open", DeploymentStatus: "failed"}}, wantPhase: githubdgrpc.CheckPhaseFailed, wantDetail: "worker deployment failed"},
		{name: "all siblings live", members: []previewMemberStatus{root, {AppID: "worker", WorkloadName: "worker", AppStatus: "active", PreviewState: "open", DeploymentStatus: "live"}}, wantPhase: githubdgrpc.CheckPhaseLive, wantDetail: "all 2 workloads"},
		{name: "missing app fails closed", members: []previewMemberStatus{root, {AppID: "worker", WorkloadName: "worker", AppStatus: "missing", DeploymentStatus: "missing"}}, wantPhase: githubdgrpc.CheckPhaseFailed, wantDetail: "worker is unavailable"},
		{name: "unexpected status fails closed", members: []previewMemberStatus{root, {AppID: "worker", WorkloadName: "worker", AppStatus: "active", PreviewState: "open", DeploymentStatus: "unknown"}}, wantPhase: githubdgrpc.CheckPhaseFailed, wantDetail: "unknown deployment status"},
		{name: "empty set fails closed", wantPhase: githubdgrpc.CheckPhaseFailed, wantDetail: "no recorded workloads"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregatePRPreviewMembers(42, "root", tc.members)
			if got.Phase != tc.wantPhase || !strings.Contains(got.Summary, tc.wantDetail) {
				t.Fatalf("aggregate = (%v, %q), want phase %v and %q", got.Phase, got.Summary, tc.wantPhase, tc.wantDetail)
			}
			if len(tc.members) > 0 && got.RootSlug != "pr-42-api" {
				t.Fatalf("root slug = %q, want pr-42-api", got.RootSlug)
			}
			if len(tc.members) > 0 && got.RootParentSlug != "api" {
				t.Fatalf("root parent = %q, want api", got.RootParentSlug)
			}
		})
	}
}

func TestCombinePreviewSetChecks_SharedSHA(t *testing.T) {
	live42 := previewPRResult{PRNumber: 42, Check: previewSetCheck{
		Phase: githubdgrpc.CheckPhaseLive, Summary: "PR #42 live", RootSlug: "pr-42-api", RootParentSlug: "api"}}
	building43 := previewPRResult{PRNumber: 43, Check: previewSetCheck{
		Phase: githubdgrpc.CheckPhaseBuilding, Summary: "PR #43 building", RootSlug: "pr-43-api"}}
	got := combinePreviewSetChecks(42, []previewPRResult{live42, building43})
	if !got.CurrentHead || got.Phase != githubdgrpc.CheckPhaseBuilding ||
		!strings.Contains(got.Summary, "1/2 PR environments") || got.CommentSummary != "PR #42 live" || got.RootSlug != "pr-42-api" {
		t.Fatalf("shared SHA partial result = %+v", got)
	}
	building43.Check.Phase, building43.Check.Summary = githubdgrpc.CheckPhaseFailed, "PR #43 failed"
	got = combinePreviewSetChecks(42, []previewPRResult{live42, building43})
	if got.Phase != githubdgrpc.CheckPhaseFailed || !strings.Contains(got.Summary, "PR #43 failed") {
		t.Fatalf("shared SHA failure result = %+v", got)
	}
	building43.Check.Phase = githubdgrpc.CheckPhaseLive
	got = combinePreviewSetChecks(42, []previewPRResult{live42, building43})
	if got.Phase != githubdgrpc.CheckPhaseLive {
		t.Fatalf("shared SHA complete result = %+v", got)
	}
	if stale := combinePreviewSetChecks(42, []previewPRResult{building43}); stale.CurrentHead {
		t.Fatalf("closed or superseded PR remained current: %+v", stale)
	}
}
