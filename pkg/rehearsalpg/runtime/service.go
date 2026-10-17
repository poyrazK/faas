package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
)

// Service is apid-owned. Owner loss cannot be adopted into complete evidence.
type Service struct {
	Catalog  rehearsalpg.Catalog
	Backend  Backend
	OwnerID  string
	mu       sync.Mutex
	sessions map[string]*session
}
type session struct {
	mu               sync.Mutex
	stop             context.CancelFunc
	done             chan struct{}
	samples, maxWait int64
	failed           bool
}

func NewService(c rehearsalpg.Catalog, b Backend) *Service {
	return &Service{Catalog: c, Backend: b, OwnerID: uuid.NewString(), sessions: map[string]*session{}}
}

// Create returns a URL only to apid's sealed-secret writer; never to the REST client.
func (s *Service) Create(ctx context.Context, account, run, app, host string, req rehearsalpg.CreateRequest) (rehearsalpg.Receipt, string, error) {
	if req.Spec.Validate() != nil || len(req.SQL) == 0 || len(req.SQL) > rehearsalpg.MaxFixtureBytes || rehearsalpg.HashBytes([]byte(req.SQL)) != req.Spec.FixtureSHA256 {
		return rehearsalpg.Receipt{}, "", ErrFixture
	}
	r := rehearsalpg.Resource{ID: uuid.NewString(), AccountID: account, RunID: run, AppID: app, OwnerID: s.OwnerID, Spec: req.Spec, Status: "provisioning", ExpiresAt: time.Now().UTC().Add(15 * time.Minute)}
	r.BackendSHA256 = s.Backend.Identity()
	r.DatabaseName = "grpg_" + strings.ReplaceAll(r.ID, "-", "")
	r.RoleName = r.DatabaseName
	r, e := s.Catalog.ReserveScenarioPostgres(ctx, r)
	if e != nil {
		return rehearsalpg.Receipt{}, "", ErrFixture
	}
	password := make([]byte, 32)
	if _, e = rand.Read(password); e != nil {
		s.failedCleanup(ctx, r)
		return rehearsalpg.Receipt{}, "", ErrFixture
	}
	secret := hex.EncodeToString(password)
	r.Initial, e = s.Backend.Provision(ctx, r, req.SQL, secret)
	if e != nil {
		s.failedCleanup(ctx, r)
		return rehearsalpg.Receipt{}, "", ErrFixture
	}
	r.Status = "ready"
	r, e = s.Catalog.TransitionScenarioPostgres(ctx, r, "provisioning")
	if e != nil {
		s.failedCleanup(ctx, r)
		return rehearsalpg.Receipt{}, "", ErrFixture
	}
	wait, e := s.Backend.LockWait(ctx, r)
	if e != nil {
		s.failedCleanup(ctx, r)
		return rehearsalpg.Receipt{}, "", ErrFixture
	}
	workerCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	worker := &session{stop: stop, done: make(chan struct{}), samples: 1, maxWait: wait}
	s.mu.Lock()
	s.sessions[r.ID] = worker
	s.mu.Unlock()
	go s.observe(workerCtx, r, worker)
	u := url.URL{Scheme: "postgresql", User: url.UserPassword(r.RoleName, secret), Host: net.JoinHostPort(host, strconv.Itoa(rehearsalpg.ProxyPort)), Path: "/" + r.DatabaseName, RawQuery: "sslmode=disable"}
	return receipt(r), u.String(), nil
}

func receipt(r rehearsalpg.Resource) rehearsalpg.Receipt {
	return rehearsalpg.Receipt{Version: 1, RunID: r.RunID, AppID: r.AppID, ResourceID: r.ID, FixtureSHA256: r.Spec.FixtureSHA256, ContractSHA256: r.Spec.ContractSHA256(), InitialSHA256: rehearsalpg.Hash(r.Initial)}
}

func (s *Service) observe(ctx context.Context, r rehearsalpg.Resource, w *session) {
	defer close(w.done)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Now().After(r.ExpiresAt) {
				w.mu.Lock()
				w.failed = true
				w.mu.Unlock()
				return
			}
			call, cancel := context.WithTimeout(ctx, time.Second)
			wait, e := s.Backend.LockWait(call, r)
			cancel()
			w.mu.Lock()
			if e != nil {
				if ctx.Err() == nil {
					w.failed = true
				}
				w.mu.Unlock()
				return
			}
			w.samples++
			if wait > w.maxWait {
				w.maxWait = wait
			}
			w.mu.Unlock()
		}
	}
}

func (s *Service) stop(id string) *session {
	s.mu.Lock()
	w := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	if w != nil {
		w.stop()
		<-w.done
	}
	return w
}

func (s *Service) Seal(ctx context.Context, account, run, deployment string) (rehearsalpg.Evidence, error) {
	r, e := s.Catalog.GetScenarioPostgres(ctx, account, run)
	if e != nil {
		return rehearsalpg.Evidence{}, ErrFixture
	}
	if r.Status == "complete" && r.Evidence != nil {
		return *r.Evidence, nil
	}
	if r.Status != "ready" || r.OwnerID != s.OwnerID || !r.ExpiresAt.After(time.Now()) {
		return rehearsalpg.Evidence{}, ErrFixture
	}
	r.Status = "sealing"
	r, e = s.Catalog.TransitionScenarioPostgres(ctx, r, "ready")
	if e != nil {
		return rehearsalpg.Evidence{}, ErrFixture
	}
	w := s.stop(r.ID)
	result := rehearsalpg.Evidence{Receipt: receipt(r), Status: "incomplete", Provenance: rehearsalpg.Provenance, DeploymentID: deployment, Spec: r.Spec, Before: r.Initial}
	result.After, e = s.Backend.FenceAndCapture(ctx, r)
	if e != nil {
		result.Reason = "database collection failed or SQL was unfinished"
	}
	if w == nil {
		result.Reason = "database collector owner was lost"
	} else {
		w.mu.Lock()
		result.LockSamples = w.samples
		result.MaxObservedLockWaitMS = w.maxWait
		if w.failed {
			result.Reason = "database lock observation was interrupted"
		}
		w.mu.Unlock()
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if s.Backend.Drop(cleanupCtx, r) != nil {
		result.Reason = "database cleanup was not confirmed"
	} else {
		result.CleanupConfirmed = true
	}
	if result.Reason != "" {
		r.Status = "failed"
		_, _ = s.Catalog.TransitionScenarioPostgres(cleanupCtx, r, "sealing")
		return result, ErrFixture
	}
	result.Changes = rehearsalpg.Changes(result.Before, result.After)
	result.LockBudgetPassed = result.MaxObservedLockWaitMS <= r.Spec.MaxLockWaitMS
	result.Status = "complete"
	if result.Validate() != nil {
		result.Status = "incomplete"
		result.Reason = "database evidence was invalid"
		r.Status = "failed"
		_, _ = s.Catalog.TransitionScenarioPostgres(cleanupCtx, r, "sealing")
		return result, ErrFixture
	}
	r.Status = "complete"
	r.Evidence = &result
	if _, e = s.Catalog.TransitionScenarioPostgres(cleanupCtx, r, "sealing"); e != nil {
		result.Status = "incomplete"
		result.Reason = "database receipt persistence failed"
		return result, ErrFixture
	}
	return result, nil
}

func (s *Service) failedCleanup(ctx context.Context, r rehearsalpg.Resource) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	current, e := s.Catalog.GetScenarioPostgres(c, r.AccountID, r.RunID)
	if e != nil {
		return
	}
	s.stop(r.ID)
	if s.Backend.Drop(c, current) != nil {
		if current.Status != "failed" {
			expected := current.Status
			current.Status = "failed"
			_, _ = s.Catalog.TransitionScenarioPostgres(c, current, expected)
		}
		return
	}
	expected := current.Status
	current.Status = "deleted"
	_, _ = s.Catalog.TransitionScenarioPostgres(c, current, expected)
}

func (s *Service) Delete(ctx context.Context, account, run string) error {
	r, e := s.Catalog.GetScenarioPostgres(ctx, account, run)
	if e != nil {
		return ErrFixture
	}
	if r.Status == "complete" || r.Status == "deleted" {
		return nil
	}
	s.stop(r.ID)
	expected := r.Status
	if expected == "ready" {
		r.Status = "sealing"
		r, e = s.Catalog.TransitionScenarioPostgres(ctx, r, expected)
		if e != nil {
			return ErrFixture
		}
		expected = "sealing"
	}
	if s.Backend.Drop(ctx, r) != nil {
		return ErrFixture
	}
	r.Status = "deleted"
	_, e = s.Catalog.TransitionScenarioPostgres(ctx, r, expected)
	return e
}

// Run keeps cleanup available even when new fixture admission is disabled.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			var workers []*session
			for _, w := range s.sessions {
				workers = append(workers, w)
			}
			s.mu.Unlock()
			for _, w := range workers {
				w.mu.Lock()
				w.failed = true
				w.mu.Unlock()
				w.stop()
			}
			return
		case <-ticker.C:
			call, cancel := context.WithTimeout(ctx, 10*time.Second)
			rows, e := s.Catalog.ExpiredScenarioPostgres(call)
			if e == nil {
				for _, r := range rows {
					_ = s.Delete(call, r.AccountID, r.RunID)
				}
			}
			cancel()
		}
	}
}
