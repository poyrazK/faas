package datapipeline

import "slices"

// PostgreSQL clears atthasmissing when a heap rewrite materializes fast-default
// values. Retain the recorded bit for an otherwise identical column so durable
// schema approvals and verification identities survive that physical change.
// Heap fingerprints still restart proofs; no data or logical DDL is assumed.
func preserveMissingMetadata(actual, recorded []relation) []relation {
	result := slices.Clone(actual)
	for i, r := range result {
		for _, old := range recorded {
			if old.Name != r.Name || old.OID != r.OID {
				continue
			}
			copied := false
			for j, c := range r.Columns {
				if c.Missing {
					continue
				}
				c.Missing = true
				if slices.Contains(old.Columns, c) {
					if !copied {
						result[i].Columns = slices.Clone(r.Columns)
						copied = true
					}
					result[i].Columns[j].Missing = true
				}
			}
			break
		}
	}
	return result
}

func recordedSourceMetadata(p Pipeline, actual []relation) []relation {
	if p.SchemaChange != nil {
		return preserveMissingMetadata(actual, p.SchemaChange.After)
	}
	return actual
}
