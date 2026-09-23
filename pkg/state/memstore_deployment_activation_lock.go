package state

import (
	"context"
	"errors"
	"strings"
	"sync"
)

type deploymentActivationLock struct {
	token chan struct{}
	users int
}

// AcquireDeploymentActivationLock mirrors PgStore's per-deployment lock for
// in-memory and concurrent handler tests. The map entry is retained only
// while a holder or waiter exists.
func (s *MemStore) AcquireDeploymentActivationLock(ctx context.Context, deploymentID string) (func(context.Context), error) {
	deploymentID = strings.TrimSpace(deploymentID)
	if deploymentID == "" {
		return nil, errors.New("state: deployment activation lock requires deployment id")
	}
	if s == nil {
		return nil, errors.New("state: nil memstore")
	}
	s.deploymentActivationMu.Lock()
	if s.deploymentActivationLocks == nil {
		s.deploymentActivationLocks = make(map[string]*deploymentActivationLock)
	}
	lock := s.deploymentActivationLocks[deploymentID]
	if lock == nil {
		lock = &deploymentActivationLock{token: make(chan struct{}, 1)}
		lock.token <- struct{}{}
		s.deploymentActivationLocks[deploymentID] = lock
	}
	lock.users++
	s.deploymentActivationMu.Unlock()

	if err := ctx.Err(); err != nil {
		s.releaseDeploymentActivationReference(deploymentID, lock)
		return nil, err
	}
	select {
	case <-ctx.Done():
		s.releaseDeploymentActivationReference(deploymentID, lock)
		return nil, ctx.Err()
	case <-lock.token:
	}
	var once sync.Once
	return func(context.Context) {
		once.Do(func() {
			lock.token <- struct{}{}
			s.releaseDeploymentActivationReference(deploymentID, lock)
		})
	}, nil
}

func (s *MemStore) releaseDeploymentActivationReference(deploymentID string, lock *deploymentActivationLock) {
	s.deploymentActivationMu.Lock()
	lock.users--
	if lock.users == 0 {
		delete(s.deploymentActivationLocks, deploymentID)
	}
	s.deploymentActivationMu.Unlock()
}

var _ DeploymentActivationLocker = (*MemStore)(nil)
var _ DeploymentActivationLocker = (*PgStore)(nil)
