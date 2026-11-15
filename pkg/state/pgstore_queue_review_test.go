//go:build !no_pg

// adr: 20261112200000
package state_test

import "testing"

func TestPgQueueLeaseSettlesAfterBindingRename(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testQueueLeaseSettlesAfterBindingRename(t, store)
}

func TestPgQueueBindingRecreateRevivesRetired(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testQueueBindingRecreateRevivesRetired(t, store)
}

func TestPgQueueRetryBackoffDoesNotExpire(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testQueueRetryBackoffDoesNotExpire(t, store)
}

func TestPgQueueSuspendedOwnerMessagesDoNotExpire(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testQueueSuspendedOwnerMessagesDoNotExpire(t, store)
}

// adr: 20261113180000
func TestPgQueueReplayClearsDeadLetterHorizon(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testQueueReplayClearsDeadLetterHorizon(t, store)
}

func TestPgQueueHeldPushMessagesKeepRetention(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testQueueHeldPushMessagesKeepRetention(t, store)
}

func TestPgQueueBindingRecreateUnderNewQueueName(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testQueueBindingRecreateUnderNewQueueName(t, store)
}
