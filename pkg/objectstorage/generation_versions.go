package objectstorage

// GenerationVersionProvider is a provider whose every object carries a
// version identifier whether or not the bucket keeps noncurrent versions:
// a GCS generation names each write even with object versioning off. Such an
// identifier is not evidence that the provider retains old versions, unlike
// an S3 version ID other than "null" (ADR-20261105120000).
type GenerationVersionProvider interface {
	GenerationVersionIDs() bool
}

// GenerationVersionIDs reports whether p's version identifiers exist on
// every object, so only versioning state or a history proof shows retained
// versions.
func GenerationVersionIDs(p any) bool {
	g, ok := p.(GenerationVersionProvider)
	return ok && g.GenerationVersionIDs()
}

// GenerationVersionIDs is true: GCS assigns every object a generation.
func (p *GCS) GenerationVersionIDs() bool { return true }

var _ GenerationVersionProvider = (*GCS)(nil)
