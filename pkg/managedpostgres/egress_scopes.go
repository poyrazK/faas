package managedpostgres

// maxRestoreChain bounds how far EgressScopeOf follows restore sources: a
// branch of a branch is the deepest copy (api.EnvironmentBranchMaxDepth), and
// the bound also stops a corrupt cycle.
const maxRestoreChain = 8

// EgressScopeOf is the database whose egress allowance databaseID shares:
// the root of its restore chain, or itself when it is not a copy.
func (s UsageSnapshot) EgressScopeOf(databaseID string) string {
	scope := databaseID
	for range maxRestoreChain {
		source, ok := s.RestoreSources[scope]
		if !ok || source == "" || source == scope {
			return scope
		}
		scope = source
	}
	return scope
}

// EgressByScope sums the month's database egress by egress scope: a source
// and its copies count once against one allowance (billing review
// 2026-11-13, R6-1).
func (s UsageSnapshot) EgressByScope() map[string]int64 {
	if len(s.DatabaseEgress) == 0 {
		return nil
	}
	out := make(map[string]int64, len(s.DatabaseEgress))
	for id, egress := range s.DatabaseEgress {
		scope := s.EgressScopeOf(id)
		out[scope] = saturatingAdd(out[scope], max(egress, 0))
	}
	return out
}

// ScopedDatabaseEgress gives every database that sent egress this month its
// scope's total, the figure its egress hold compares with the allowance: a
// copy is held once it and its source together sent the allowance.
func (s UsageSnapshot) ScopedDatabaseEgress() map[string]int64 {
	if len(s.DatabaseEgress) == 0 {
		return nil
	}
	scopes := s.EgressByScope()
	out := make(map[string]int64, len(s.DatabaseEgress))
	for id := range s.DatabaseEgress {
		out[id] = scopes[s.EgressScopeOf(id)]
	}
	return out
}
