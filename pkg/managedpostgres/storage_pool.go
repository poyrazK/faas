package managedpostgres

// SpecForObservation returns a provider's observed spec as the stored spec
// expected reads it. The storage pool (ADR-20261025094915 decision 9) keeps an
// account inside its pooled storage by lowering each database's provider
// storage limit, and only ever lowers it, so a limit between zero and the
// spec's is enforcement state, not drift. Zero, which Neon reads as no limit,
// a limit above the spec's, and every other difference still read as drift.
// The history limit (ADR-20261123062111 D2) likewise shortens a database's
// restore window to the limited window while its subject is over the limit,
// so exactly that window, below the spec's, is enforcement state too.
func SpecForObservation(expected, observed Spec) Spec {
	if observed.StorageLimitBytes > 0 && observed.StorageLimitBytes < expected.StorageLimitBytes {
		observed.StorageLimitBytes = expected.StorageLimitBytes
	}
	if observed.RestoreWindowSeconds == historyLimitedSeconds && historyLimitedSeconds < expected.RestoreWindowSeconds {
		observed.RestoreWindowSeconds = expected.RestoreWindowSeconds
	}
	return observed
}
