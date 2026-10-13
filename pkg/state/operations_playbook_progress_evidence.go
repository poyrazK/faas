package state

import (
	"context"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func preparePlaybookProgressEvidence(ctx context.Context, store workflowCaseBackend, r workflowPlaybookProgressRecord, refs []api.OperationWorkflowPlaybookEvidence) ([]api.OperationWorkflowPlaybookEvidence, error) {
	out := []api.OperationWorkflowPlaybookEvidence{}
	seen := map[string]bool{}
	for _, ref := range refs {
		id, err := uuid.Parse(ref.OperationID)
		if err != nil || id == uuid.Nil {
			return nil, ErrInvalidArgument
		}
		ref.OperationID = id.String()
		key := ref.OperationID + "/" + ref.MilestoneID
		if seen[key] {
			return nil, ErrInvalidArgument
		}
		seen[key] = true
		op, err := store.OperationByID(ctx, r.Account, r.Tenant, ref.OperationID)
		if err != nil {
			return nil, err
		}
		if !sameOperationHistoryIdentity(op.AppID, r.App) || op.Scope != r.Scope || !sameOperationHistoryIdentity(op.PlatformTenantID, r.Tenant) || op.Subject == nil || op.Subject.Type != r.SubjectType || op.Subject.ID != r.SubjectID {
			return nil, ErrNotFound
		}
		if ref.MilestoneID != "" {
			if err = validatePlaybookMilestoneEvidence(ctx, store, r, ref); err != nil {
				return nil, err
			}
		}
		out = append(out, ref)
	}
	return out, nil
}
func validatePlaybookMilestoneEvidence(ctx context.Context, store workflowCaseBackend, r workflowPlaybookProgressRecord, ref api.OperationWorkflowPlaybookEvidence) error {
	// Query only this operation and exact business reference. Paginate bounded retained facts.
	opts := api.OperationMilestoneListOptions{AppID: r.App, Scope: r.Scope, OperationID: ref.OperationID, SubjectType: r.SubjectType, SubjectID: r.SubjectID, Limit: api.OperationHistoryPageMax}
	for scanned := 0; scanned < api.OperationMilestonesMaxPerOperation; {
		page, err := store.ListPlatformTenantOperationMilestones(ctx, r.Account, r.Tenant, opts)
		if err != nil {
			return err
		}
		for _, m := range page.Milestones {
			scanned++
			if m.ID == ref.MilestoneID {
				for _, s := range m.WorkflowSteps {
					if s.Workflow == r.Workflow && s.InstanceID == r.InstanceID && effectiveWorkflowContractVersion(s.Version) == r.Progress.ContractVersion {
						return nil
					}
				}
				return ErrInvalidArgument
			}
		}
		if page.NextCursor == "" || len(page.Milestones) == 0 {
			break
		}
		opts.Cursor = page.NextCursor
	}
	return ErrNotFound
}
