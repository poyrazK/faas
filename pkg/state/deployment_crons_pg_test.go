//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestDeploymentCronJournalPG(t *testing.T) { store, _ := pgStore(t); testCronJournal(t, store) }
func TestDeploymentCronJournalPGConflicts(t *testing.T) {
	testCronConflicts(t, func(t *testing.T) state.Store { store, _ := pgStore(t); return store })
}
func TestDeploymentCronJournalPGConcurrency(t *testing.T) {
	store, _ := pgStore(t)
	testCronRace(t, store)
}
func TestDeploymentCronJournalPGOrdinaryEditRace(t *testing.T) {
	store, _ := pgStore(t)
	testCronOrdinaryEditRace(t, store)
}
func TestDeploymentCronJournalPGAccountQuotaRace(t *testing.T) {
	store, _ := pgStore(t)
	testCronAccountQuotaRace(t, store)
}
func TestDeploymentCronJournalPGCommandIdentityCollision(t *testing.T) {
	store, _ := pgStore(t)
	testCronIdentityCollision(t, store)
}

func TestDeploymentCronActivationPG(t *testing.T) {
	testCronActivation(t, func(t *testing.T) state.Store { store, _ := pgStore(t); return store })
}
