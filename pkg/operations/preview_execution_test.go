// adr: 604 — native admission requires an explicit execution allowlist.
package operations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPreviewExecutionAllowlist(t *testing.T) {
	now := time.Now().UTC()
	c := PreviewCohort{AccountID: uuid.NewString(), AppID: uuid.NewString(), Scope: "production", PlatformTenantIDs: []string{uuid.NewString()}}
	p := PreviewPolicy{Version: 1, Enabled: true, NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute), Cohorts: []PreviewCohort{c}}
	path := filepath.Join(t.TempDir(), "preview.json")
	g, _ := NewPreviewAdmission(path)
	g.Now = func() time.Time { return now }
	for _, kinds := range [][]string{nil, {ExecutionHTTP}, {ExecutionWorkflow}, {ExecutionJob}, {ExecutionHTTP, ExecutionWorkflow, ExecutionJob}} {
		p.Cohorts[0].ExecutionKinds = kinds
		raw, _ := json.Marshal(p)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{ExecutionHTTP, ExecutionWorkflow, ExecutionJob, "", "HTTP", "*"} {
			want := kind == ExecutionHTTP && kinds == nil
			for _, allowed := range kinds {
				want = want || allowed == kind
			}
			o := g.ObserveKind(c.AccountID, c.AppID, c.Scope, c.PlatformTenantIDs[0], kind)
			if o.Allowed != want || g.AllowsTenantKind(c.AccountID, c.AppID, c.Scope, c.PlatformTenantIDs[0], kind) != want || g.AllowsDefinitionKinds(c.AccountID, c.AppID, c.Scope, []string{kind}) != want || o.ObservedAt != now {
				t.Fatalf("allowlist %v kind %q observation %+v want %v", kinds, kind, o, want)
			}
			if g.AllowsTenantKind(c.AccountID, c.AppID, c.Scope, uuid.NewString(), kind) || g.AllowsTenantKind(c.AccountID, c.AppID, "staging", c.PlatformTenantIDs[0], kind) || g.AllowsTenantKind(c.AccountID, c.AppID, c.Scope, "", kind) {
				t.Fatal("execution grant widened cohort identity")
			}
		}
		if g.AllowsDefinitionKinds(c.AccountID, c.AppID, c.Scope, []string{ExecutionHTTP, ExecutionWorkflow, ExecutionJob}) != (len(kinds) == 3) {
			t.Fatal("mixed registration bypassed its complete allowlist")
		}
		if g.AllowsDefinitionKinds(c.AccountID, c.AppID, c.Scope, nil) {
			t.Fatal("empty admission request granted authority")
		}
	}
	// An observation remains an honest snapshot, while the next admission sees
	// atomic rollback of only the native families without closing HTTP.
	snapshot := g.ObserveCohort(c.AccountID, c.AppID, c.Scope, c.PlatformTenantIDs[0])
	p.Cohorts[0].ExecutionKinds = []string{ExecutionHTTP}
	raw, _ := json.Marshal(p)
	if err := os.WriteFile(path+".next", raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".next", path); err != nil {
		t.Fatal(err)
	}
	if !snapshot.ForExecutionKind(ExecutionJob).Allowed || g.AllowsTenantKind(c.AccountID, c.AppID, c.Scope, c.PlatformTenantIDs[0], ExecutionJob) || !g.AllowsTenant(c.AccountID, c.AppID, c.Scope, c.PlatformTenantIDs[0]) {
		t.Fatal("rollback reused cached authority or disabled HTTP")
	}
	// Malformed allowlists close the entire policy, including otherwise valid
	// cohorts; an explicit empty array cannot be mistaken for the default.
	for _, value := range []string{`[]`, `["http","http"]`, `["native"]`, `["HTTP"]`, `["*"]`, `"job"`, `[1]`} {
		bad := strings.Replace(string(raw), `["http"]`, value, 1)
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if o := g.Observe(c.AccountID, c.AppID, c.Scope, c.PlatformTenantIDs[0]); o.Allowed || o.Code != "preview_policy_unavailable" {
			t.Fatalf("invalid allowlist %s did not fail closed: %+v", value, o)
		}
	}
}

func TestDefinitionExecutionKind(t *testing.T) {
	for _, tc := range []struct {
		spec api.OperationDefinitionSpec
		want string
	}{
		{api.OperationDefinitionSpec{}, ExecutionHTTP},
		{api.OperationDefinitionSpec{TransactionReceipt: "postgres_v1"}, ExecutionHTTP},
		{api.OperationDefinitionSpec{Job: "export-job"}, ExecutionJob},
		{api.OperationDefinitionSpec{Workflow: "export-chain"}, ExecutionWorkflow},
		{api.OperationDefinitionSpec{Job: "export-job", Workflow: "export-chain"}, ""},
		{api.OperationDefinitionSpec{Job: "export-job", TransactionReceipt: "postgres_v1"}, ""},
		{api.OperationDefinitionSpec{Workflow: "export-chain", TransactionReceipt: "postgres_v1"}, ""},
	} {
		if got := DefinitionExecutionKind(tc.spec); got != tc.want {
			t.Fatalf("kind %q want %q for %+v", got, tc.want, tc.spec)
		}
	}
}
