package datapipeline

import (
	"context"
	"errors"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
)

func SetupInput(r api.DataPipelineSetupRequest) SetupRequest {
	return SetupRequest{Name: r.Name, SourceConnectionID: r.SourceConnectionID, DatabaseID: r.DatabaseID, Region: r.Region, Tables: r.Tables, ReadSubjects: r.ReadSubjects, Serving: r.Serving, FreshnessTargetSeconds: r.FreshnessTargetSeconds, DataAPIName: r.DataAPIName, AuthAppID: r.AuthAppID}
}

func (r SetupRequest) PublicRequest() api.DataPipelineSetupRequest {
	return api.DataPipelineSetupRequest{Name: r.Name, SourceConnectionID: r.SourceConnectionID, DatabaseID: r.DatabaseID, Region: r.Region, Tables: r.Tables, ReadSubjects: setupCreationSubjects(r), Serving: r.Serving, FreshnessTargetSeconds: r.FreshnessTargetSeconds, DataAPIName: r.DataAPIName, AuthAppID: r.AuthAppID}
}

// PlanSetup calls only read-only planning callbacks; it never enqueues intent or
// calls EnsureMirror, PrepareDataAPI or DeployDataAPI.
func (s *Service) PlanSetup(ctx context.Context, account string, r SetupRequest) (api.DataPipelineSetupPlan, error) {
	var out api.DataPipelineSetupPlan
	if err := s.admit(ctx, account); err != nil {
		return out, err
	}
	r = normalizeSetupIDs(r)
	input := api.CreateDataPipelineRequest{Name: r.Name, SourceConnectionID: r.SourceConnectionID, DatabaseID: r.DatabaseID, Tables: r.Tables, ReadSubjects: r.ReadSubjects, Serving: r.Serving, FreshnessTargetSeconds: r.FreshnessTargetSeconds}
	if input.DatabaseID == "" {
		input.DatabaseID = account
	}
	if err := ValidateCreate(input); err != nil {
		return out, err
	}
	if len(r.Region) > 63 || r.DatabaseID != "" && r.Region != "" || (r.DataAPIName == "") != (r.AuthAppID == "") || r.DataAPIName != "" && (!api.ValidAppSlug(r.DataAPIName) || api.IsReservedAppSlug(r.DataAPIName) || !validID(r.AuthAppID)) {
		return out, ErrInvalid
	}
	n := normalize(input)
	r.Tables, r.ReadSubjects, r.Serving = n.Tables, n.ReadSubjects, n.Serving
	store, err := s.setupStore()
	if err != nil {
		return out, err
	}
	out = api.DataPipelineSetupPlan{Name: r.Name, CreatesMirror: r.DatabaseID == "", CreatesDataAPI: r.DataAPIName != "", DatabaseID: r.DatabaseID, FreshnessTargetSeconds: r.FreshnessTargetSeconds, BillingNotes: []string{"No mirror or app is created by this plan. Capacity admission is rechecked before allocation.", "Mirror storage, restore retention and app compute are billed under your account's plan and actual usage; this is not an invoice or fixed-price quote.", "An active logical-replication subscriber can keep source compute running. Source-provider charges are separate and are not estimated here."}}
	old, err := store.SetupByName(ctx, account, r.Name)
	if err == nil {
		if !reflect.DeepEqual(old.Request, r) {
			return out, ErrConflict
		}
		out.ExistingSetupID, out.DatabaseID = old.ID, old.DatabaseID
		out.CreatesMirror, out.CreatesDataAPI = false, false
		out.Region, out.StorageLimitBytes, out.RestoreWindowSeconds = old.MirrorPlan.Region, old.MirrorPlan.StorageLimitBytes, old.MirrorPlan.RestoreWindowSeconds
		out.BillingNotes = append(out.BillingNotes, "Resume the saved job to reuse its resource IDs. A partially provisioned resource may already incur charges.")
		return out, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return out, err
	}
	check, err := s.Store.Check(ctx, account, r.SourceConnectionID)
	if err != nil {
		return out, ErrCheckRequired
	}
	selection := check.DataConnectionCheck
	selection.Fresh, selection.State = true, "passed"
	if err = ValidateServingSelection(selection, r.Tables, r.Serving); err != nil {
		return out, ErrCheckRequired
	}
	out.ConnectionCheck = &check.DataConnectionCheck
	if r.DatabaseID == "" {
		if s.PlanMirror == nil {
			return out, ErrUnavailable
		}
		plan, err := s.PlanMirror(ctx, account, r.Region)
		if err != nil {
			return out, err
		}
		out.Region, out.StorageLimitBytes, out.RestoreWindowSeconds = plan.Region, plan.StorageLimitBytes, plan.RestoreWindowSeconds
	} else {
		if s.ValidateTarget == nil {
			return out, ErrUnavailable
		}
		if err = s.ValidateTarget(ctx, account, r.DatabaseID); err != nil {
			return out, err
		}
		if s.PlanExistingMirror != nil {
			plan, err := s.PlanExistingMirror(ctx, account, r.DatabaseID)
			if err != nil {
				return out, err
			}
			out.Region, out.StorageLimitBytes, out.RestoreWindowSeconds = plan.Region, plan.StorageLimitBytes, plan.RestoreWindowSeconds
		}
	}
	if r.DataAPIName != "" {
		if s.PlanDataAPI == nil {
			return out, ErrUnavailable
		}
		if _, err = s.PlanDataAPI(ctx, account, r); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (s *Service) ControlSetup(ctx context.Context, account, id string, revision int64, action string) (Setup, error) {
	if s == nil || s.Store == nil || s.Allowed == nil || !s.Allowed(ctx, account) {
		return Setup{}, ErrUnavailable
	}
	if action == "retry" {
		if err := s.admit(ctx, account); err != nil {
			return Setup{}, err
		}
	} else if action != "cancel" {
		return Setup{}, ErrInvalid
	}
	if !validID(account) || !validID(id) || revision < 1 {
		return Setup{}, ErrInvalid
	}
	store, err := s.setupStore()
	if err != nil {
		return Setup{}, err
	}
	return store.ControlSetup(ctx, account, id, revision, action)
}

func (s *Service) Setups(ctx context.Context, account string) ([]Setup, error) {
	if s == nil || s.Allowed == nil || !s.Allowed(ctx, account) {
		return nil, ErrUnavailable
	}
	if !validID(account) {
		return nil, ErrInvalid
	}
	store, err := s.setupStore()
	if err != nil {
		return nil, err
	}
	return store.Setups(ctx, account)
}
func (s *Service) GetSetup(ctx context.Context, account, id string) (Setup, error) {
	if s == nil || s.Allowed == nil || !s.Allowed(ctx, account) {
		return Setup{}, ErrUnavailable
	}
	if !validID(account) || !validID(id) {
		return Setup{}, ErrInvalid
	}
	store, err := s.setupStore()
	if err != nil {
		return Setup{}, err
	}
	return store.Setup(ctx, account, id)
}
