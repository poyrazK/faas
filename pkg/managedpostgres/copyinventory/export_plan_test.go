// adr:567
package copyinventory

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func exportPlanInventory(t *testing.T) Inventory {
	t.Helper()
	cfg, i := sealedInventoryValue(t)
	locale, localeName, version := "i", "en-US", "153.120"
	i.body.Databases = []Database{
		{OID: 12, Name: "private_customer", OwnerOID: 1, Owner: cfg.RoleName, TablespaceOID: 3, Encoding: 6, ConnectionLimit: 7,
			Collation: "en_US.UTF-8", CType: "en_US.UTF-8", ACL: []string{"private_member=c/private_role"}, LocaleProvider: &locale, Locale: &localeName, CollationVersion: &version},
		i.body.Databases[0],
		{OID: 4, Name: "template0", OwnerOID: 1, Owner: cfg.RoleName, TablespaceOID: 3, Template: true},
		{OID: 7, Name: "template1", OwnerOID: 1, Owner: cfg.RoleName, TablespaceOID: 3, Template: true, AllowConnections: true},
		{OID: 9, Name: "private_maintenance", OwnerOID: 1, Owner: cfg.RoleName, TablespaceOID: 3},
		{OID: 11, Name: "private_already_closed", OwnerOID: 1, Owner: cfg.RoleName, TablespaceOID: 3},
	}
	i.body.Databases[1].AllowConnections = true
	i.body.Roles = append(i.body.Roles, Role{OID: 22, Name: "private_member", Login: true, Inherit: false, ConnectionLimit: 5})
	i.body.Memberships = []Membership{{RoleOID: 1, Role: cfg.RoleName, MemberOID: 22, Member: "private_member", GrantorOID: 1, Grantor: cfg.RoleName, Admin: true, Set: true}}
	i.body.Settings = []Setting{{DatabaseOID: 12, Database: "private_customer", RoleOID: 22, Role: "private_member", Config: []string{"app.api_key=private-config-secret"}}}
	i.body.Tablespaces[0].ACL = []string{"private_member=C/private_role"}
	i.body.Tablespaces[0].Options = []string{"random_page_cost=1.1"}
	if !validPayload(i.body) {
		t.Fatal(payloadProblem(i.body))
	}
	mac, err := fingerprintPayload(i.body, cfg.FingerprintKey)
	if err != nil {
		t.Fatal(err)
	}
	i.fingerprint = hex.EncodeToString(mac)
	return i
}

func TestExportPlanCaptureIdentityRetainsOriginalAdmissionProjection(t *testing.T) {
	i := exportPlanInventory(t)
	scope := sealedInventoryScope(16)
	base, err := i.PlanExports(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := i.PlanExports(scope, []OriginalAdmission{{DatabaseOID: 12, OwnerOID: 1, DatabaseName: "private_customer", OriginalAllowConnections: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !base.SameCaptureForWorker(projected) || !projected.SameCaptureForWorker(base) {
		t.Fatal("original logical admission changed captured identity")
	}
	changed := base
	changed.captured.PostgresMajor++
	if base.SameCaptureForWorker(changed) {
		t.Fatal("substituted capture accepted with matching metadata fingerprint")
	}
	changed = base
	changed.scope.SourceVersion = strings.Repeat("f", 64)
	if base.SameCaptureForWorker(changed) || base.SameCaptureForWorker(ExportPlan{}) {
		t.Fatal("different scope or empty plan accepted")
	}
}

func TestExportPlanRetainsEveryDatabaseAndProjectsOnlyOriginalAdmission(t *testing.T) {
	i := exportPlanInventory(t)
	original, _ := i.PayloadForSealing()
	p, err := i.PlanExports(sealedInventoryScope(16), []OriginalAdmission{
		{DatabaseOID: 12, OwnerOID: 1, DatabaseName: "private_customer", OriginalAllowConnections: true},
		{DatabaseOID: 11, OwnerOID: 1, DatabaseName: "private_already_closed", OriginalAllowConnections: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := p.Summary()
	if s.InventoryFingerprint != i.fingerprint || s.Databases != 6 || s.Templates != 2 || s.AdmissionProjections != 4 || s.Roles != 2 || s.Memberships != 1 || s.Settings != 1 || s.Tablespaces != 1 || s.PreparedTransactions != 0 {
		t.Fatal("export plan omitted catalogue requirements")
	}
	requirements, err := p.RequirementsForWorker()
	if err != nil || len(requirements) != 6 {
		t.Fatalf("worker requirements: %v", err)
	}
	for n, id := range []uint32{2, 4, 7, 9, 11, 12} {
		d := requirements[n]
		if d.Database.OID != id || d.AuthenticatedReaderRoleOID != 1 || d.InventoryFingerprint != i.fingerprint || !d.Scope.Equal(p.scope) || d.AuthenticatedReaderDatabase != (id == 2) {
			t.Fatal("database identity or reader pins omitted")
		}
	}
	customer := requirements[5]
	if customer.CapturedAllowConnections || !customer.Database.AllowConnections || customer.Database.ConnectionLimit != 7 || customer.Database.LocaleProvider == nil || *customer.Database.LocaleProvider != "i" {
		t.Fatal("source fence changed logical configuration or lost locale")
	}
	if requirements[1].Database.AllowConnections || requirements[4].Database.AllowConnections || !requirements[1].Database.Template {
		t.Fatal("originally closed database or template was rewritten")
	}
	raw, err := p.LogicalMetadataForSealing()
	if err != nil {
		t.Fatal(err)
	}
	var logical payload
	if json.Unmarshal(raw, &logical) != nil {
		t.Fatal("invalid projected metadata")
	}
	want := i.body
	want.Databases = slices.Clone(i.body.Databases)
	slices.SortFunc(want.Databases, func(a, b Database) int { return int(a.OID) - int(b.OID) })
	want.Databases[5].AllowConnections = true
	if !reflect.DeepEqual(logical, want) {
		t.Fatal("projected metadata changed more than original admission and order")
	}
	after, _ := i.PayloadForSealing()
	if !bytes.Equal(original, after) {
		t.Fatal("plan mutated frozen inventory")
	}
	// Both the source inventory and each worker result are independent copies.
	i.body.Roles[0].Config[0] = "changed"
	i.body.Databases[0].ACL[0] = "changed"
	*customer.Database.LocaleProvider = "changed"
	requirements[5].Database.ACL[0] = "changed"
	next, _ := p.RequirementsForWorker()
	nextMetadata, _ := p.LogicalMetadataForSealing()
	if *next[5].Database.LocaleProvider != "i" || next[5].Database.ACL[0] == "changed" || !bytes.Equal(raw, nextMetadata) {
		t.Fatal("caller mutation changed retained plan")
	}
}

func TestExportPlanRejectsUnmatchedAdmissionAndInvalidInventory(t *testing.T) {
	i := exportPlanInventory(t)
	base := OriginalAdmission{DatabaseOID: 12, OwnerOID: 1, DatabaseName: "private_customer", OriginalAllowConnections: true}
	for _, tc := range []struct {
		name string
		edit func(*OriginalAdmission)
	}{
		{"database_oid", func(a *OriginalAdmission) { a.DatabaseOID = 99 }},
		{"owner_oid", func(a *OriginalAdmission) { a.OwnerOID = 22 }},
		{"database_name", func(a *OriginalAdmission) { a.DatabaseName = "private_already_closed" }},
		{"empty_name", func(a *OriginalAdmission) { a.DatabaseName = "" }},
		{"zero_owner", func(a *OriginalAdmission) { a.OwnerOID = 0 }},
		{"not_closed", func(a *OriginalAdmission) { a.DatabaseOID, a.DatabaseName = 2, "private_database" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := base
			tc.edit(&changed)
			if _, err := i.PlanExports(sealedInventoryScope(16), []OriginalAdmission{changed}); !errors.Is(err, pgerrors.ErrConflict) {
				t.Fatalf("unmatched admission: %v", err)
			}
		})
	}
	if _, err := i.PlanExports(sealedInventoryScope(16), []OriginalAdmission{base, base}); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatalf("duplicate admission: %v", err)
	}
	if _, err := i.PlanExports(sealedInventoryScope(17), nil); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatalf("wrong major: %v", err)
	}
	if _, err := i.PlanExports(Scope{}, nil); !errors.Is(err, pgerrors.ErrInvalid) {
		t.Fatalf("invalid scope: %v", err)
	}
	if _, err := (Inventory{}).PlanExports(sealedInventoryScope(16), nil); !errors.Is(err, pgerrors.ErrInvalid) {
		t.Fatalf("empty inventory: %v", err)
	}
	if _, err := (ExportPlan{}).RequirementsForWorker(); !errors.Is(err, pgerrors.ErrInvalid) {
		t.Fatalf("empty plan: %v", err)
	}
	if _, err := (ExportPlan{}).LogicalMetadataForSealing(); !errors.Is(err, pgerrors.ErrInvalid) {
		t.Fatalf("empty metadata: %v", err)
	}
}

func TestExportPlanPreparedTransactionsBlockAllDatabaseExports(t *testing.T) {
	i := exportPlanInventory(t)
	i.body.PreparedTransactions = []PreparedTransaction{{Transaction: "17", GID: "private-prepared", Prepared: "2026-10-03T00:00:00Z", Owner: "private_role", Database: "private_already_closed"}}
	p, err := i.PlanExports(sealedInventoryScope(16), nil)
	if err != nil || p.Summary().PreparedTransactions != 1 {
		t.Fatalf("prepared transaction inventory: %v", err)
	}
	if req, err := p.RequirementsForWorker(); !errors.Is(err, pgerrors.ErrUnsupported) || req != nil {
		t.Fatalf("prepared transaction allowed partial export: %v", err)
	}
	raw, err := p.LogicalMetadataForSealing()
	if err != nil || !bytes.Contains(raw, []byte("private-prepared")) {
		t.Fatalf("prepared transaction omitted from encrypted input: %v", err)
	}
}

func TestExportPlanOrdinaryOutputRedactsPrivateCatalogue(t *testing.T) {
	p, err := exportPlanInventory(t).PlanExports(sealedInventoryScope(16), nil)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := p.RequirementsForWorker()
	for _, value := range []any{p, req[5]} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, out := range []string{string(raw), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			for _, secret := range []string{"private_customer", "private_role", "private_member", "private-config-secret", "en_US.UTF-8"} {
				if strings.Contains(out, secret) {
					t.Fatal("ordinary output exposed private catalogue")
				}
			}
		}
	}
}
