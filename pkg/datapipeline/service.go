package datapipeline

import (
	"context"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type Service struct {
	PlanDataAPI    func(context.Context, string, SetupRequest) (DataAPIPlan, error)
	PrepareDataAPI func(context.Context, Setup) (string, bool, error)
	DeployDataAPI  func(context.Context, Setup) (string, string, error)
	DataAPIState   func(context.Context, Setup) (string, string, error)
	PlanMirror     func(context.Context, string, string) (MirrorPlan, error)
	EnsureMirror   func(context.Context, Setup) error
	MirrorState    func(context.Context, string, string) (string, error)
	Store          Store
	Recipient      *age.X25519Recipient
	Allowed        func(context.Context, string) bool
	CanCreate      func(context.Context, string) bool
	ValidateTarget func(context.Context, string, string) error
	CapacityStatus func(context.Context, string, string) (*api.DataPipelineCapacity, error)
}

func (s *Service) Available(ctx context.Context, account string) bool {
	return s.admit(ctx, account) == nil
}

func (s *Service) Get(ctx context.Context, account, id string) (api.DataPipeline, error) {
	p, err := s.Store.Get(ctx, account, id)
	if err != nil {
		return api.DataPipeline{}, err
	}
	if s.CapacityStatus != nil && p.State != "deleted" {
		p.Capacity, err = s.CapacityStatus(ctx, account, id)
	}
	return p.DataPipeline, err
}

func (s *Service) Control(ctx context.Context, account, id string, r api.ControlDataPipelineRequest) (api.DataPipeline, error) {
	if s == nil || s.Store == nil {
		return api.DataPipeline{}, ErrUnavailable
	}
	if r.Action == "resume" || r.Action == "resync" || r.Action == "verify" {
		if s.Allowed == nil || !s.Allowed(ctx, account) || ((r.Action == "resume" || r.Action == "resync") && s.CanCreate != nil && !s.CanCreate(ctx, account)) {
			return api.DataPipeline{}, ErrUnavailable
		}
	}
	p, err := s.Store.Control(ctx, account, id, r.ExpectedRevision, r.Action)
	return p.DataPipeline, err
}

func (s *Service) admit(ctx context.Context, account string) error {
	if s == nil || s.Store == nil || s.Allowed == nil || !s.Allowed(ctx, account) || (s.CanCreate != nil && !s.CanCreate(ctx, account)) {
		return ErrUnavailable
	}
	return nil
}
func (s *Service) CreateConnection(ctx context.Context, account string, r api.CreateDataConnectionRequest) (api.DataConnection, error) {
	if err := s.admit(ctx, account); err != nil {
		return api.DataConnection{}, err
	}
	if !api.ValidAppSlug(r.Name) {
		return api.DataConnection{}, ErrInvalid
	}
	id := uuid.NewString()
	blob, identity, err := sealConnection(s.Recipient, account, id, r.ConnectionURL)
	if err != nil {
		return api.DataConnection{}, err
	}
	c, err := s.Store.CreateConnection(ctx, Connection{DataConnection: api.DataConnection{ID: id, Name: r.Name, Revision: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}, AccountID: account, Ciphertext: blob, Identity: identity})
	return c.DataConnection, err
}
func (s *Service) RotateConnection(ctx context.Context, account, id string, r api.RotateDataConnectionRequest) (api.DataConnection, error) {
	if s == nil || s.Store == nil {
		return api.DataConnection{}, ErrUnavailable
	}
	old, err := s.Store.Connection(ctx, account, id)
	if err != nil {
		return api.DataConnection{}, err
	}
	blob, identity, err := sealConnection(s.Recipient, account, id, r.ConnectionURL)
	if err != nil {
		return api.DataConnection{}, err
	}
	if identity != old.Identity {
		return api.DataConnection{}, ErrConflict
	}
	old.Ciphertext = blob
	c, err := s.Store.RotateConnection(ctx, old, r.ExpectedRevision)
	return c.DataConnection, err
}
func (s *Service) Create(ctx context.Context, account string, r api.CreateDataPipelineRequest) (api.DataPipeline, error) {
	if err := s.admit(ctx, account); err != nil {
		return api.DataPipeline{}, err
	}
	if err := ValidateCreate(r); err != nil {
		return api.DataPipeline{}, err
	}
	if s.ValidateTarget == nil {
		return api.DataPipeline{}, ErrUnavailable
	}
	if err := s.ValidateTarget(ctx, account, r.DatabaseID); err != nil {
		return api.DataPipeline{}, err
	}
	r = normalize(r)
	p, err := s.Store.Create(ctx, Pipeline{AccountID: account, DataPipeline: api.DataPipeline{ID: uuid.NewString(), Name: r.Name, SourceConnectionID: r.SourceConnectionID, DatabaseID: r.DatabaseID, Tables: r.Tables, ReadSubjects: r.ReadSubjects, DesiredState: "running", State: "validating", Revision: 1, Generation: 1, Verification: "unknown", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}})
	return p.DataPipeline, err
}
