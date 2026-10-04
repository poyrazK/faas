//go:build !no_pg

// adr: 568
package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

type clonePublicationLeaseWaitStore struct {
	cloneWorkloadTestStore
	pool   *pgxpool.Pool
	t      *testing.T
	expire bool
}

func (s *clonePublicationLeaseWaitStore) PublishProjectEnvironmentCloneReleaseSet(ctx context.Context, accountID, projectID, operationID string, revision int64, ttl int) (state.ProjectReleaseSet, error) {
	if !s.expire {
		return s.cloneWorkloadTestStore.PublishProjectEnvironmentCloneReleaseSet(ctx, accountID, projectID, operationID, revision, ttl)
	}
	s.expire = false
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	holder, err := s.pool.Begin(bounded)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := holder.Exec(bounded, "select id from project_environments where project_id=$1 and slug='stage' for update", projectID); err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.pool.Exec(bounded, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '200 milliseconds' where id=$1", operationID); err != nil {
		s.t.Fatal(err)
	}
	type result struct {
		release state.ProjectReleaseSet
		err     error
	}
	done := make(chan result, 1)
	go func() {
		release, err := s.cloneWorkloadTestStore.PublishProjectEnvironmentCloneReleaseSet(bounded, accountID, projectID, operationID, revision, ttl)
		done <- result{release, err}
	}()
	waited := false
	for until := time.Now().Add(time.Second); time.Now().Before(until); {
		if err := s.pool.QueryRow(bounded, `select lease_until<=clock_timestamp() and exists(
			select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock'
			and position('LockFeatureFlagEnvironment' in query)>0)
			from project_environment_clone_operations where id=$1`, operationID).Scan(&waited); err != nil {
			s.t.Fatal(err)
		}
		if waited {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !waited {
		s.t.Fatal("did not observe publication waiting for the flag lock past its server-clock lease expiry")
	}
	if err := holder.Rollback(bounded); err != nil {
		s.t.Fatal(err)
	}
	finished := <-done
	return finished.release, finished.err
}

func TestPgProjectEnvironmentClonePublicationRejectsLeaseExpiredDuringFlagLockWait(t *testing.T) {
	s, _, pool := pgWithPool(t)
	waiting := &clonePublicationLeaseWaitStore{cloneWorkloadTestStore: s, pool: pool, t: t}
	projectEnvironmentClonePublicationContract(t, waiting, false, "after_publication_lease_wait", func(context.Context, string) error {
		waiting.expire = true
		return nil
	})
}
