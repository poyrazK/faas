package rootfs

// PasswdResolver is the production Resolver implementation
// (ADR-142 §Decision 2). It wraps a parsed /etc/passwd map and
// satisfies Resolver. Construction is via NewPasswdResolver.
//
// Thread-safe: PasswdResolver is constructed once at full-rootfs
// build time and read-only after that. The map is owned by the
// resolver and not exposed.
type PasswdResolver struct {
	entries map[string]PasswdEntry
}

// NewPasswdResolver builds a PasswdResolver from a parsed /etc/passwd
// map (typically the result of ParsePasswd on the image's merged
// passwd table). Empty map is allowed and produces a no-op resolver.
func NewPasswdResolver(entries map[string]PasswdEntry) *PasswdResolver {
	return &PasswdResolver{entries: entries}
}

// Resolve returns (uid, gid, true) when the name is present in the
// parsed table; (0, 0, false) otherwise. The caller (preserveOwnership)
// applies the existing inOwnershipRange clamp and increments the
// unparseable_uid counter on miss — PasswdResolver itself is a pure
// lookup with no policy.
//
// ADR-142 §Rejected: looking up /etc/passwd from the host is
// rejected because the host's table is unrelated to the image's.
// ADR-142 §Rejected: an empty /etc/passwd map still produces a
// well-formed resolver; every lookup miss returns ok=false and
// the caller falls through to today's unparseable_uid counter +
// daemon uid.
func (p *PasswdResolver) Resolve(name string) (int, int, bool) {
	if p == nil || p.entries == nil {
		return 0, 0, false
	}
	e, ok := p.entries[name]
	if !ok {
		return 0, 0, false
	}
	return e.Uid, e.Gid, true
}
