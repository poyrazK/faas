package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type previewMemberStatus struct {
	AppID            string
	Slug             string
	PreviewOfSlug    string
	WorkloadName     string
	AppStatus        string
	PreviewState     string
	DeploymentStatus string
}

type previewSetCheck struct {
	Phase          githubdgrpc.CheckPhase
	Summary        string
	CommentSummary string
	RootSlug       string
	RootParentSlug string
	CurrentHead    bool
}

type previewPRResult struct {
	PRNumber int
	Check    previewSetCheck
}

// loadPRPreviewSetCheck reads the current head's exact expected workload set.
// Old-head notifications and pre-migration previews are no-ops: neither may
// update the shared check from one app's deployment state alone.
func loadPRPreviewSetCheck(ctx context.Context, pool *pgxpool.Pool, installationID int64, repo string, prNumber int, commitSHA string) (previewSetCheck, error) {
	set, err := state.NewPgStore(pool).GetPRPreviewSet(ctx, installationID, repo, prNumber)
	if errors.Is(err, state.ErrNotFound) {
		return previewSetCheck{}, nil
	}
	if err != nil {
		return previewSetCheck{}, err
	}
	if set.Closed || set.CommitSHA != commitSHA {
		return previewSetCheck{}, nil
	}
	// GitHub identifies the shared `gregale-preview` check by repo, SHA, and
	// name, not PR number. Two PRs can point at the same commit, so that check
	// must represent every open preview set for the commit. The PR comment
	// still reports only its own environment.
	setRows, err := pool.Query(ctx, `select pr_number, root_app_id::text, member_app_ids
		from pr_preview_sets where installation_id = $1 and repo_full_name = $2
		  and commit_sha = $3 and closed_at is null order by pr_number`, installationID, repo, commitSHA)
	if err != nil {
		return previewSetCheck{}, fmt.Errorf("githubd: load preview sets for commit: %w", err)
	}
	var sets []state.PRPreviewSet
	for setRows.Next() {
		var candidate state.PRPreviewSet
		if err := setRows.Scan(&candidate.PRNumber, &candidate.RootAppID, &candidate.MemberAppIDs); err != nil {
			setRows.Close()
			return previewSetCheck{}, fmt.Errorf("githubd: scan preview set for commit: %w", err)
		}
		sets = append(sets, candidate)
	}
	if err := setRows.Err(); err != nil {
		setRows.Close()
		return previewSetCheck{}, fmt.Errorf("githubd: iterate preview sets for commit: %w", err)
	}
	setRows.Close()
	results := make([]previewPRResult, 0, len(sets))
	for _, candidate := range sets {
		members, err := loadPRPreviewMembers(ctx, pool, candidate.RootAppID, candidate.PRNumber, candidate.MemberAppIDs, commitSHA)
		if err != nil {
			return previewSetCheck{}, err
		}
		results = append(results, previewPRResult{PRNumber: candidate.PRNumber,
			Check: aggregatePRPreviewMembers(candidate.PRNumber, candidate.RootAppID, members)})
	}
	return combinePreviewSetChecks(prNumber, results), nil
}

// combinePreviewSetChecks keeps the fixed-name GitHub check honest even when
// multiple PRs share one commit. The comment remains specific to the PR that
// triggered this projection.
func combinePreviewSetChecks(prNumber int, results []previewPRResult) previewSetCheck {
	check := previewSetCheck{Phase: githubdgrpc.CheckPhaseLive}
	readySets := 0
	waitingPR := 0
	failedSummary := ""
	for _, result := range results {
		part := result.Check
		if result.PRNumber == prNumber {
			check.CurrentHead = true
			check.RootSlug = part.RootSlug
			check.RootParentSlug = part.RootParentSlug
			check.CommentSummary = part.Summary
		}
		switch part.Phase {
		case githubdgrpc.CheckPhaseLive:
			readySets++
		case githubdgrpc.CheckPhaseFailed:
			if failedSummary == "" {
				failedSummary = part.Summary
			}
		default:
			if waitingPR == 0 {
				waitingPR = result.PRNumber
			}
		}
	}
	if !check.CurrentHead {
		return previewSetCheck{} // PR closed while this projection was loading
	}
	if len(results) == 1 {
		check.Summary = check.CommentSummary
		if failedSummary != "" {
			check.Phase = githubdgrpc.CheckPhaseFailed
		} else if readySets == 0 {
			check.Phase = githubdgrpc.CheckPhaseBuilding
		}
		return check
	}
	switch {
	case failedSummary != "":
		check.Phase = githubdgrpc.CheckPhaseFailed
		check.Summary = fmt.Sprintf("Shared preview commit failed: %s", failedSummary)
	case readySets == len(results):
		check.Summary = fmt.Sprintf("Shared preview commit live: all %d PR environments ready.", len(results))
	default:
		check.Phase = githubdgrpc.CheckPhaseBuilding
		check.Summary = fmt.Sprintf("Shared preview commit building: %d/%d PR environments ready; waiting for PR #%d.", readySets, len(results), waitingPR)
	}
	return check
}

func loadPRPreviewMembers(ctx context.Context, pool *pgxpool.Pool, rootAppID string, prNumber int, memberIDs []string, commitSHA string) ([]previewMemberStatus, error) {
	rows, err := pool.Query(ctx, `
		select expected.app_id,
		       coalesce(a.slug, ''), coalesce(a.preview_of_slug, ''),
		       coalesce(nullif(a.workload_name, ''), a.slug, expected.app_id),
		       coalesce(a.status, 'missing'), coalesce(a.preview_pr_state, ''),
		       coalesce(d.status, 'missing')
		from unnest($1::text[]) with ordinality as expected(app_id, ordinal)
		left join apps root on root.id = $3::uuid
		left join apps a on a.id = expected.app_id::uuid
		  and a.account_id = root.account_id
		  and a.project_id is not distinct from root.project_id
		  and a.preview_pr_number = $4
		left join lateral (
			select status from deployments
			where app_id = a.id and kind = 'preview' and commit_sha = $2
			order by created_at desc, id desc limit 1
		) d on true
		order by expected.ordinal`, memberIDs, commitSHA, rootAppID, prNumber)
	if err != nil {
		return nil, fmt.Errorf("githubd: load PR preview members: %w", err)
	}
	defer rows.Close()
	members := make([]previewMemberStatus, 0, len(memberIDs))
	for rows.Next() {
		var member previewMemberStatus
		if err := rows.Scan(&member.AppID, &member.Slug, &member.PreviewOfSlug, &member.WorkloadName,
			&member.AppStatus, &member.PreviewState, &member.DeploymentStatus); err != nil {
			return nil, fmt.Errorf("githubd: scan PR preview member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("githubd: iterate PR preview members: %w", err)
	}
	return members, nil
}

// aggregatePRPreviewMembers is fail-closed. A missing or failed sibling takes
// precedence over a live root; no event delivery order can make a partial
// environment green. Only deployments for the recorded head reach this input.
func aggregatePRPreviewMembers(prNumber int, rootAppID string, members []previewMemberStatus) previewSetCheck {
	check := previewSetCheck{Phase: githubdgrpc.CheckPhaseBuilding}
	if len(members) == 0 {
		check.Phase = githubdgrpc.CheckPhaseFailed
		check.Summary = fmt.Sprintf("Preview PR #%d has no recorded workloads.", prNumber)
		return check
	}
	ready := 0
	waiting := make([]string, 0, 3)
	failed := ""
	for _, member := range members {
		if member.AppID == rootAppID {
			check.RootSlug = member.Slug
			check.RootParentSlug = member.PreviewOfSlug
		}
		name := strings.Join(strings.Fields(member.WorkloadName), " ")
		if name == "" {
			name = member.AppID
		}
		switch {
		case (member.AppStatus != string(state.AppActive) && member.AppStatus != string(state.AppEvictedCold)) || member.PreviewState != state.PreviewPrStateOpen:
			if failed == "" {
				failed = fmt.Sprintf("%s is unavailable", name)
			}
		case member.DeploymentStatus == string(state.DeployFailed) || member.DeploymentStatus == string(state.DeployCancelled) || member.DeploymentStatus == string(state.DeploySuperseded):
			if failed == "" {
				failed = fmt.Sprintf("%s deployment %s", name, member.DeploymentStatus)
			}
		case member.DeploymentStatus == string(state.DeployLive):
			ready++
		case member.DeploymentStatus == "missing" || member.DeploymentStatus == string(state.DeployPending) ||
			member.DeploymentStatus == string(state.DeployBuilding) || member.DeploymentStatus == string(state.DeployImaging) ||
			member.DeploymentStatus == string(state.DeploySnapshotting):
			if len(waiting) < 3 {
				waiting = append(waiting, name)
			}
		default:
			if failed == "" {
				failed = fmt.Sprintf("%s has unknown deployment status", name)
			}
		}
	}
	if failed != "" {
		check.Phase = githubdgrpc.CheckPhaseFailed
		check.Summary = fmt.Sprintf("Preview PR #%d failed: %s. %d/%d workloads live.", prNumber, failed, ready, len(members))
		return check
	}
	if ready == len(members) {
		check.Phase = githubdgrpc.CheckPhaseLive
		check.Summary = fmt.Sprintf("Preview PR #%d live: all %d workloads reached this commit.", prNumber, ready)
		return check
	}
	check.Summary = fmt.Sprintf("Preview PR #%d building: %d/%d workloads live; waiting for %s.",
		prNumber, ready, len(members), strings.Join(waiting, ", "))
	return check
}
