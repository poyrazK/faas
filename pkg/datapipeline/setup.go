package datapipeline

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
)

// SetupRequest is immutable customer intent, containing no credentials.
// Empty DatabaseID requests a new dedicated mirror.
type SetupRequest struct {
	Name               string
	SourceConnectionID string
	DatabaseID         string
	Region             string
	Tables             []string
	ReadSubjects       []string
	DataAPIName        string
	AuthAppID          string
}

// DataAPIPlan contains only metadata. Credentials remain in managed bindings
// and the ordinary encrypted app-secret store.
type DataAPIPlan struct {
	AppID, AuthAppID, Issuer, JWKSURL, Audience string
	BindingID, IntentID, DeploymentID, BuildID  string
	PreviousDeploymentID, PreviousBuildID       string
	Endpoint                                    string
}

// MirrorPlan freezes server-selected defaults before requesting provision.
type MirrorPlan struct {
	Region               string
	StorageLimitBytes    int64
	RestoreWindowSeconds int64
}
type Setup struct {
	ID, AccountID, DatabaseID, MirrorName, PipelineID      string
	Request                                                SetupRequest
	MirrorPlan                                             MirrorPlan
	DataAPIPlan                                            DataAPIPlan
	State, Phase, Reason, LeaseToken, CreatedAt, UpdatedAt string
	Revision                                               int64
	LeaseUntil                                             time.Time
}
type SetupStore interface {
	EnqueueSetup(context.Context, Setup) (Setup, error)
	SetupByName(context.Context, string, string) (Setup, error)
	Setup(context.Context, string, string) (Setup, error)
	Setups(context.Context, string) ([]Setup, error)
	ControlSetup(context.Context, string, string, int64, string) (Setup, error)
	ClaimSetup(context.Context, string) (Setup, error)
	FinishSetup(context.Context, Setup, Setup) error
}

func (s *Service) setupStore() (SetupStore, error) {
	if s == nil || s.Store == nil {
		return nil, ErrUnavailable
	}
	store, ok := s.Store.(SetupStore)
	if !ok {
		return nil, ErrUnavailable
	}
	return store, nil
}

func (s *Service) StartSetup(ctx context.Context, account string, r SetupRequest) (Setup, error) {
	if err := s.admit(ctx, account); err != nil {
		return Setup{}, err
	}
	if s.PlanMirror == nil || s.EnsureMirror == nil || s.MirrorState == nil {
		return Setup{}, ErrUnavailable
	}
	target := r.DatabaseID
	if target == "" {
		target = uuid.NewString()
	}
	normalized := normalize(api.CreateDataPipelineRequest{Name: r.Name, SourceConnectionID: r.SourceConnectionID, DatabaseID: target, Tables: r.Tables, ReadSubjects: r.ReadSubjects})
	if err := ValidateCreate(normalized); err != nil {
		return Setup{}, err
	}
	if len(r.Region) > 63 || (r.DatabaseID != "" && r.Region != "") {
		return Setup{}, ErrInvalid
	}
	r.Tables, r.ReadSubjects = normalized.Tables, normalized.ReadSubjects
	if (r.DataAPIName == "") != (r.AuthAppID == "") || (r.DataAPIName != "" && (!api.ValidAppSlug(r.DataAPIName) || api.IsReservedAppSlug(r.DataAPIName) || !validID(r.AuthAppID))) {
		return Setup{}, ErrInvalid
	}
	store, err := s.setupStore()
	if err != nil {
		return Setup{}, err
	}
	old, err := store.SetupByName(ctx, account, r.Name)
	if err == nil {
		if !reflect.DeepEqual(old.Request, r) {
			return Setup{}, ErrConflict
		}
		return old, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Setup{}, err
	}
	check, err := s.Store.Check(ctx, account, r.SourceConnectionID)
	if err != nil {
		return Setup{}, ErrCheckRequired
	}
	// Submission saves selections already discovered for this owned source.
	// Evidence may expire while the user is editing. The worker revalidates
	// current credentials, compatibility and capacity before provisioning.
	selection := check.DataConnectionCheck
	selection.Fresh, selection.State = true, "passed"
	if checkSelection(selection, r.Tables) != nil {
		return Setup{}, ErrCheckRequired
	}
	setup := Setup{ID: uuid.NewString(), AccountID: account, Request: r, DatabaseID: target}
	if r.DataAPIName != "" {
		if s.PlanDataAPI == nil || s.PrepareDataAPI == nil || s.DeployDataAPI == nil || s.DataAPIState == nil {
			return Setup{}, ErrUnavailable
		}
		setup.DataAPIPlan, err = s.PlanDataAPI(ctx, account, r)
		if err != nil {
			return Setup{}, err
		}
		setup.DataAPIPlan.IntentID = uuid.NewString()
	}
	setup.MirrorName = r.Name + "-mirror-" + setup.ID[:8]
	if r.DatabaseID == "" {
		setup.MirrorPlan, err = s.PlanMirror(ctx, account, r.Region)
		if err != nil {
			return Setup{}, err
		}
	}
	return store.EnqueueSetup(ctx, setup)
}

func sourceSetupCheck(c api.DataConnectionCheck, tables []string) error {
	if err := checkSelection(c, tables); err != nil {
		return err
	}
	if c.Capacity == nil || c.Capacity.Source == nil {
		return connectioncapacity.ErrUnknown
	}
	if c.Capacity.Source.State == "insufficient_capacity" {
		return connectioncapacity.ErrInsufficient
	}
	if c.Capacity.Source.State != "ready" {
		return connectioncapacity.ErrUnknown
	}
	return nil
}

// SetupTick advances a single persisted step. Remote/provider calls occur only
// after the claim transaction commits. IDs and definitions survive lost replies.
func (s *Service) SetupTick(ctx context.Context) error {
	store, err := s.setupStore()
	if err != nil {
		return err
	}
	job, err := store.ClaimSetup(ctx, uuid.NewString())
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	op, cancel := context.WithTimeout(ctx, api.DataPipelineOperationTimeout)
	defer cancel()
	next := job
	if err = s.admit(op, job.AccountID); err == nil {
		current, getErr := store.Setup(op, job.AccountID, job.ID)
		if getErr != nil || current.Revision != job.Revision || current.LeaseToken != job.LeaseToken || !current.LeaseUntil.After(time.Now()) {
			return ErrConflict
		}
		err = s.advanceSetup(op, &next)
	}
	if err != nil {
		next.State, next.Reason = "blocked", setupErrorCode(err)
	}
	return store.FinishSetup(ctx, job, next)
}

func (s *Service) advanceSetup(ctx context.Context, job *Setup) error {
	switch job.Phase {
	case "checking_source":
		ready, err := s.setupCheck(ctx, *job, "")
		if err != nil {
			return err
		}
		if ready {
			job.Phase = "provisioning"
		}
	case "provisioning":
		if job.Request.DatabaseID == "" {
			ready, err := s.setupCheck(ctx, *job, "")
			if err != nil {
				return err
			}
			if !ready {
				job.Phase = "checking_source"
				return nil
			}
			if s.EnsureMirror == nil {
				return ErrUnavailable
			}
			if err := s.EnsureMirror(ctx, *job); err != nil {
				return err
			}
		}
		if s.MirrorState == nil {
			return ErrUnavailable
		}
		state, err := s.MirrorState(ctx, job.AccountID, job.DatabaseID)
		if err != nil {
			return err
		}
		switch state {
		case "ready":
			job.Phase = "checking_mirror"
		case "failed", "deleting", "deleted":
			return &blocked{code: "mirror_provisioning_failed"}
		}
	case "checking_mirror":
		ready, err := s.setupCheck(ctx, *job, job.DatabaseID)
		if err != nil {
			return err
		}
		if ready {
			job.Phase = "starting"
		}
	case "starting":
		p, err := s.Create(ctx, job.AccountID, api.CreateDataPipelineRequest{Name: job.Request.Name, SourceConnectionID: job.Request.SourceConnectionID, DatabaseID: job.DatabaseID, Tables: job.Request.Tables, ReadSubjects: job.Request.ReadSubjects})
		if errors.Is(err, ErrCheckRequired) {
			job.Phase = "checking_mirror"
			return nil
		}
		if err != nil {
			return err
		}
		job.PipelineID, job.Phase = p.ID, "syncing"
	case "syncing":
		p, err := s.Store.Get(ctx, job.AccountID, job.PipelineID)
		if err != nil {
			return err
		}
		if p.DesiredState != "running" || p.State == "blocked" || p.State == "resync_required" {
			return &blocked{code: "pipeline_needs_attention"}
		}
		if p.State == "live" && p.ObservationFresh {
			if job.Request.DataAPIName == "" {
				job.State = "ready"
			} else {
				job.Phase = "configuring_api"
			}
		}
	case "configuring_api", "deploying_api", "waiting_api", "api_failed":
		return s.advanceDataAPI(ctx, job)
	default:
		return ErrInvalid
	}
	return nil
}

func (s *Service) setupCheck(ctx context.Context, job Setup, database string) (bool, error) {
	connection, err := s.Store.Connection(ctx, job.AccountID, job.Request.SourceConnectionID)
	if err != nil {
		return false, err
	}
	check, err := s.Store.Check(ctx, job.AccountID, connection.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if err == nil && check.ConnectionRevision == connection.Revision && check.DatabaseID == database && check.Fresh && setupCapacityEvidenceFresh(check.DataConnectionCheck, time.Now()) {
		if check.State != "passed" {
			return false, &blocked{code: "connection_check_blocked"}
		}
		if err = sourceSetupCheck(check.DataConnectionCheck, job.Request.Tables); err != nil {
			return false, err
		}
		if database != "" && (check.Capacity == nil || check.Capacity.State != "ready") {
			return false, connectioncapacity.ErrUnknown
		}
		return true, nil
	}
	_, err = s.Store.RequestCheck(ctx, job.AccountID, connection.ID, connection.Revision, database)
	return false, err
}

func setupErrorCode(err error) string {
	var b *blocked
	if errors.As(err, &b) {
		return b.code
	}
	switch {
	case errors.Is(err, connectioncapacity.ErrInsufficient):
		return "insufficient_capacity"
	case errors.Is(err, connectioncapacity.ErrUnknown):
		return "capacity_needs_qualification"
	case errors.Is(err, ErrConflict):
		return "resource_conflict"
	case errors.Is(err, ErrCheckRequired):
		return "connection_check_required"
	case errors.Is(err, ErrQuota):
		return "account_limit"
	case errors.Is(err, ErrInvalid):
		return "setup_configuration_invalid"
	case errors.Is(err, ErrUnavailable):
		return "setup_unavailable"
	default:
		return "setup_step_failed"
	}
}

// Capacity proofs expire sooner than compatibility metadata. Expiry asks for a
// new observation; a fresh negative/unknown proof remains an actionable blocker.
func setupCapacityEvidenceFresh(check api.DataConnectionCheck, now time.Time) bool {
	if check.Capacity == nil {
		return true
	}
	for _, endpoint := range []*api.DataEndpointCapacity{check.Capacity.Source, check.Capacity.Destination} {
		if endpoint == nil || endpoint.ExpiresAt == "" {
			continue
		}
		expires, err := time.Parse(time.RFC3339Nano, endpoint.ExpiresAt)
		if err != nil || !now.Before(expires) {
			return false
		}
	}
	return true
}
