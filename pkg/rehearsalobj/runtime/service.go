// Package runtime owns private S3 fixture execution and independent collection.
package runtime

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	"github.com/onebox-faas/faas/pkg/state"
)

type Service struct{ Catalog rehearsalobj.Catalog }

func NewService(c rehearsalobj.Catalog) *Service { return &Service{Catalog: c} }

func (s *Service) Create(ctx context.Context, account, run, app, access string, sealed []byte, req rehearsalobj.CreateRequest) (out rehearsalobj.Receipt, err error) {
	if e := req.Validate(); e != nil {
		return rehearsalobj.Receipt{}, e
	}
	initial, _ := rehearsalobj.Snapshot(req.Objects)
	r, e := s.Catalog.ReserveScenarioObjects(ctx, rehearsalobj.Resource{ID: uuid.NewString(), AccountID: account, RunID: run, AppID: app, Status: "ready", AccessKey: access, SecretSealed: sealed, ExpiresAt: time.Now().Add(14 * time.Minute), Spec: req.Spec, Initial: initial, Current: req.Objects, Attempts: []rehearsalobj.Attempt{}})
	if e != nil {
		return r.Receipt(), e
	}
	defer func() {
		if err != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			_ = s.Delete(cleanup, account, run)
		}
	}()
	readyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for r.CollectorID == "" && !r.Failed {
		select {
		case <-readyCtx.Done():
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = s.Delete(cleanup, account, run)
			stop()
			return rehearsalobj.Receipt{}, errors.New("object collector did not acknowledge the namespace")
		case <-ticker.C:
		}
		r, e = s.Catalog.GetScenarioObjects(readyCtx, account, run)
		if e != nil {
			return rehearsalobj.Receipt{}, e
		}
	}
	if r.Status != "ready" || r.Failed {
		return rehearsalobj.Receipt{}, errors.New("object collector is unavailable")
	}
	return r.Receipt(), nil
}

func (s *Service) Seal(ctx context.Context, account, run, deployment string) (out rehearsalobj.Evidence, err error) {
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = s.Delete(cleanup, account, run)
		}
	}()

	r, e := s.Catalog.UpdateScenarioObjects(ctx, account, run, "", func(r *rehearsalobj.Resource) error {
		if r.Status == "complete" {
			if r.Evidence == nil || r.Evidence.DeploymentID != deployment {
				return state.ErrConflict
			}
			return nil
		}
		if (r.Status != "ready" && r.Status != "sealing") || !r.ExpiresAt.After(time.Now()) {
			return state.ErrConflict
		}
		r.Status = "sealing"
		return nil
	})
	if e != nil {
		return rehearsalobj.Evidence{}, e
	}
	if r.Status == "complete" {
		return *r.Evidence, nil
	}
	joinCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		pending := !r.CollectorClosed
		for _, a := range r.Attempts {
			pending = pending || a.Pending
		}
		if !pending {
			break
		}
		select {
		case <-joinCtx.Done():
			return rehearsalobj.Evidence{}, errors.New("object requests did not finish before collection")
		case <-ticker.C:
		}
		r, e = s.Catalog.GetScenarioObjects(joinCtx, account, run)
		if e != nil {
			return rehearsalobj.Evidence{}, e
		}
	}
	bindings, err := s.deploymentBindings(ctx, account, run)
	if err != nil {
		return rehearsalobj.Evidence{}, err
	}
	r, e = s.Catalog.UpdateScenarioObjects(ctx, account, run, "", func(r *rehearsalobj.Resource) error {
		if r.Status == "complete" && r.Evidence != nil && r.Evidence.DeploymentID == deployment {
			return nil
		}
		if r.Status != "sealing" || r.Failed || !r.CollectorClosed || r.CollectorID == "" || !r.ExpiresAt.After(time.Now()) {
			return state.ErrConflict
		}
		mutations := make([]rehearsalobj.Mutation, 0)
		for _, a := range r.Attempts {
			if a.Pending || !validObjectAttempt(a, *r, deployment, bindings) {
				return state.ErrConflict
			}
			if a.Mutation != nil {
				mutations = append(mutations, *a.Mutation)
			}
		}
		sort.Slice(mutations, func(i, j int) bool { return mutations[i].Sequence < mutations[j].Sequence })
		after, err := rehearsalobj.Snapshot(r.Current)
		if err != nil {
			return err
		}
		evidence := rehearsalobj.Evidence{Receipt: r.Receipt(), Status: "complete", Provenance: rehearsalobj.Provenance, DeploymentID: deployment, Spec: r.Spec, Before: r.Initial, After: after, Mutations: mutations, CleanupConfirmed: true, CollectorClosed: r.CollectorClosed}
		if err = evidence.Validate(); err != nil {
			return err
		}
		r.Current = []rehearsalobj.Data{}
		r.Status = "complete"
		r.Evidence = &evidence
		return nil
	})
	if e != nil {
		return rehearsalobj.Evidence{}, e
	}
	return *r.Evidence, nil
}
func (s *Service) Delete(ctx context.Context, account, run string) error {
	_, e := s.Catalog.UpdateScenarioObjects(ctx, account, run, "", func(r *rehearsalobj.Resource) error {
		if r.Status == "complete" || r.Status == "deleted" {
			return nil
		}
		r.Current = []rehearsalobj.Data{}
		r.Status = "deleted"
		return nil
	})
	if errors.Is(e, state.ErrNotFound) {
		return nil
	}
	return e
}

// Cleanup stays active when creation is disabled. Expired admission and late
// request completion are refused even while a cleanup retry is outstanding.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		resources, e := s.Catalog.ExpiredScenarioObjects(ctx)
		if e != nil {
			continue
		}
		for _, r := range resources {
			cleanup, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = s.Delete(cleanup, r.AccountID, r.RunID)
			cancel()
		}
	}
}
