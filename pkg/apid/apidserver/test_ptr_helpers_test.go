package apidserver

// intPtr is shared by the internal tests (edge rules, export_test.go). It
// used to live in grace_cache_test.go, which went with the grace-window cache
// (ADR-1237).
func intPtr(i int) *int { return &i }
