package state_test

import (
	"testing"
	"time"
)

// Issue #3360: the runtime-config change stamp must round-trip through
// Postgres and advance on every mutation, because schedd compares it with
// instances.started_at (also stamped by the database clock).
func TestPg_AppRuntimeConfigChanges(t *testing.T) {
	s, ctx := pgStore(t)
	_, app := seedPgApp(t, s, ctx)

	if _, ok, err := s.AppRuntimeConfigChangedAt(ctx, app.ID); err != nil || ok {
		t.Fatalf("fresh app = (ok=%v, err=%v), want no stamp", ok, err)
	}
	if err := s.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatalf("MarkAppRuntimeConfigChanged: %v", err)
	}
	first, ok, err := s.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil || !ok {
		t.Fatalf("after mark = (ok=%v, err=%v), want stamped", ok, err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := s.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
		t.Fatalf("second MarkAppRuntimeConfigChanged: %v", err)
	}
	second, _, err := s.AppRuntimeConfigChangedAt(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppRuntimeConfigChangedAt: %v", err)
	}
	if !second.After(first) {
		t.Fatalf("second stamp %v not after first %v", second, first)
	}
}
