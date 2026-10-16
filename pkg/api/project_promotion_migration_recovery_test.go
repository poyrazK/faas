package api

import (
	"strings"
	"testing"
)

func TestPromotionRecoveryRequestBindsExplicitInspection(t *testing.T) {
	base := ProjectPromotionMigrationRecoveryRequest{RequestID: strings.Repeat("a", 32), ExpectedTaskID: "00000000-0000-0000-0000-000000000001", ExpectedGeneration: 4, Action: "resume", DatabaseStateChecked: true, SchemaCompatible: true, EvidenceHash: strings.Repeat("b", 64)}
	if !base.Valid() {
		t.Fatal("valid resume rejected")
	}
	for _, mutate := range []func(*ProjectPromotionMigrationRecoveryRequest){func(r *ProjectPromotionMigrationRecoveryRequest) { r.SchemaCompatible = false }, func(r *ProjectPromotionMigrationRecoveryRequest) { r.DatabaseStateChecked = false }, func(r *ProjectPromotionMigrationRecoveryRequest) { r.ExpectedGeneration = 0 }, func(r *ProjectPromotionMigrationRecoveryRequest) { r.ExpectedTaskID = "task" }, func(r *ProjectPromotionMigrationRecoveryRequest) { r.Action = "replay" }, func(r *ProjectPromotionMigrationRecoveryRequest) { r.EvidenceHash = "private notes" }} {
		r := base
		mutate(&r)
		if r.Valid() || r.Fingerprint() == base.Fingerprint() {
			t.Fatal("unbound or invalid recovery accepted")
		}
	}
	repair := base
	repair.Action = "resolve"
	repair.SchemaCompatible = false
	if repair.Valid() {
		t.Fatal("resolve accepted without repair identity")
	}
	repair.ReplacementPromotionID = "00000000-0000-0000-0000-000000000002"
	if !repair.Valid() {
		t.Fatal("valid repair rejected")
	}
}
