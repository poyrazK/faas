package databaseswitch

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// View renders a switch for the API. revision resolves a deployment ID to its
// vN revision; nil leaves revisions out.
func View(sw state.DatabaseSwitch, revision func(id string) int) api.DatabaseSwitch {
	out := api.DatabaseSwitch{
		ID: sw.ID, AppID: sw.AppID, Scope: sw.Scope, Phase: sw.Phase,
		FromDeploymentID: sw.FromDeploymentID, TargetDeploymentID: sw.TargetDeploymentID,
		UndoneDeploymentIDs: append([]string{}, sw.UndoneDeploymentIDs...),
		SourceDatabaseID:    sw.SourceDatabaseID, TargetDatabaseID: sw.TargetDatabaseID, TargetName: sw.TargetName,
		RestorePoint: stamp(sw.RestorePoint),
		Drain: api.DatabaseSwitchDrain{Retired: sw.Drain.Retired, Parked: sw.Drain.Parked,
			Pending: sw.Drain.Pending, Blocked: sw.Drain.Blocked},
		FailureCode: sw.FailureCode, FailureDetail: sw.FailureDetail, LastErrorCode: sw.LastErrorCode,
		RequestedBy: sw.RequestedBy, CancelRequestedAt: stampPtr(sw.CancelRequestedAt), FencedAt: stampPtr(sw.FencedAt),
		ReleaseSwitchedAt: stampPtr(sw.ReleaseSwitchedAt), ActivatedAt: stampPtr(sw.ActivatedAt),
		FinishedAt: stampPtr(sw.FinishedAt), PhaseStartedAt: stamp(sw.PhaseStartedAt),
		CreatedAt: stamp(sw.CreatedAt), UpdatedAt: stamp(sw.UpdatedAt),
	}
	if revision != nil {
		if sw.FromDeploymentID != "" {
			out.FromRevision = revision(sw.FromDeploymentID)
		}
		out.TargetRevision = revision(sw.TargetDeploymentID)
	}
	return out
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func stampPtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return stamp(*t)
}
