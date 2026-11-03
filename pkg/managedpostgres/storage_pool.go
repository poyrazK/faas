package managedpostgres

// SpecForObservation returns a provider's observed spec as the stored spec
// expected reads it. The storage pool (ADR-20261025094915 decision 9) keeps an
// account inside its pooled storage by lowering each database's provider
// storage limit, and only ever lowers it, so a limit between zero and the
// spec's is enforcement state, not drift. Zero, which Neon reads as no limit,
// a limit above the spec's, and every other difference still read as drift.
func SpecForObservation(expected, observed Spec) Spec {
	if observed.StorageLimitBytes > 0 && observed.StorageLimitBytes < expected.StorageLimitBytes {
		observed.StorageLimitBytes = expected.StorageLimitBytes
	}
	return observed
}
