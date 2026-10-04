package state_test

import "testing"

func TestPgStoreListenerAppRetirement(t *testing.T) {
	store, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx, "-listener-retirement")
	checkListenerRetirement(t, store, ctx, accountID, appID)
}
