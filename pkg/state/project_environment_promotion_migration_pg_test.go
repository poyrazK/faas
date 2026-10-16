//go:build !no_pg

package state

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"sync"
	"testing"
)

func TestPromotionMigrationPG(t *testing.T) {
	testPromotionMigration(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}

func TestPromotionMigrationPGRacesOrdinaryReleaseTask(t *testing.T) {
	store := NewPgStore(pgtest.OpenMigrated(t))
	f := newPromotionMigrationFixture(t, store)
	var promotionErr, taskErr error
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, _, promotionErr = store.CreateProjectEnvironmentPromotion(t.Context(), f.promotion, f.workloads)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, taskErr = store.CreateAppTask(t.Context(), CreateAppTaskParams{AccountID: f.account.ID, AppID: f.apps[0].ID, DeploymentID: f.sources[0].ID, Kind: AppTaskKindRelease, Command: f.sources[0].ReleaseCommand, CommandShell: true})
	}()
	close(start)
	wg.Wait()
	if (promotionErr == nil) == (taskErr == nil) {
		t.Fatalf("expected one migration admission, promotion=%v task=%v", promotionErr, taskErr)
	}
}
