//go:build metal && function_background_native

package e2e_test

// adr: 20261018030615

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/functionmigration"
	"github.com/onebox-faas/faas/pkg/e2etest/functionsql"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type nativeDestinationFixture struct {
	config  functionmigration.Config
	store   *state.PgStore
	h       *e2etest.Harness
	key     string
	slug    string
	appID   string
	sql     *functionsql.Fixture
	proxy   postgresfixture.Proxy
	control *functionsql.Control
	run     *functionmigration.Run
	ledger  *sched.NodeLedger
}

func newNativeDestinationFixture(t *testing.T, c functionmigration.Config, catalog *pgxpool.Pool, runtime string) *nativeDestinationFixture {
	t.Helper()
	f := &nativeDestinationFixture{config: c, store: state.NewPgStore(catalog), run: &functionmigration.Run{Runtime: runtime}}
	f.h = &e2etest.Harness{T: t, Pool: catalog, APIDURL: c.APIURL, GatewayURL: c.GatewayURL}
	label := "migration-" + uuid.NewString()
	f.key = f.h.SeedAccount(t.Context(), api.PlanPro, label)
	t.Cleanup(func() {
		if !f.run.FinalAppRetired || !f.run.SourceRetired {
			return // Retain ambiguous migration ownership for fixture recovery.
		}
		account, err := f.store.AccountByEmail(context.WithoutCancel(t.Context()), "e2e+pro+"+label+"@test.example")
		if err != nil || f.store.DeleteAccount(context.WithoutCancel(t.Context()), account.ID) != nil {
			t.Error("remove retired native fixture account")
		}
	})
	verifyNativeCatalogAPI(t, f)
	f.sql = functionsql.New(t)
	f.proxy = postgresfixture.OpenProxy(t, f.sql.Admin, c.SQLIP, 0)
	f.control = functionsql.NewControl(t, c.SQLIP)
	f.slug = "migration-" + randHexSuffix()
	public := false
	if status := postOK(t, f.h, f.key, "/v1/apps", api.CreateAppRequest{Slug: f.slug, Type: "function", Runtime: runtime, RequireAuthn: &public, FunctionConcurrency: 1, MaxConcurrency: 1}); status != http.StatusCreated {
		t.Fatal("create native migration function")
	}
	f.appID = mustGetAppID(t, f.h, f.key, f.slug)
	t.Cleanup(func() {
		f.control.Release()
		cleanupNativeMigrationApp(t, f.h, f.key, f.slug, f.appID, f.store, f.run)
		if f.run.FinalAppRetired && f.ledger != nil {
			f.ledger.Release(f.run.InstanceID)
		}
	})
	ports := []int{int(f.proxy.Port), int(f.control.Port)}
	if _, status := doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+f.slug, api.UpdateAppRequest{EgressPorts: &ports}); status != http.StatusOK {
		t.Fatal("configure isolated migration SQL egress")
	}
	for name, value := range map[string]string{"DATABASE_URL": f.sql.ConnectionString(f.proxy.Host, f.proxy.Port), "NODE_EXTRA_CA_CERTS": "/app/sql-ca.pem",
		"FUNCTION_SQL_CONTROL_URL": f.control.URL, "FUNCTION_SQL_CONTROL_TOKEN": f.control.Token} {
		if _, status := doReq(t, f.h, f.key, http.MethodPut, "/v1/apps/"+f.slug+"/secrets/"+name, api.PutAppSecretRequest{Value: value}); status != http.StatusOK {
			t.Fatal("seal native migration SQL inputs")
		}
	}
	enrollNativeSQLMigration(t, f.store, f.appID, c.SQLIP)
	return f
}

// Before any API mutation, a read must authenticate with a fresh random key
// written only into this disposable catalog. Another catalog rejects the key
// before any customer intent is written there.
func verifyNativeCatalogAPI(t *testing.T, f *nativeDestinationFixture) {
	t.Helper()
	if _, status := doReq(t, f.h, f.key, http.MethodGet, "/v1/apps", nil); status != http.StatusOK {
		t.Fatal("API did not authenticate the disposable catalog's fresh fixture key")
	}
}

func (f *nativeDestinationFixture) deploy(t *testing.T, runtime string) string {
	t.Helper()
	ca, err := os.ReadFile(f.config.SQLCAPath)
	if err != nil || len(ca) == 0 {
		t.Fatal("native migration SQL CA is unavailable")
	}
	files := functionsql.Files(t)
	files["sql-ca.pem"] = string(ca)
	body, status := postBinaryFunctionDeployment(t, f.h, f.key, f.slug, buildTarGz(t, files), runtime, "handler.handler")
	if status != http.StatusAccepted {
		t.Fatal("deploy native migration function")
	}
	dep, _ := parseQueuedDeployment(t, body)
	ctx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer cancel()
	if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, f.h.Pool, dep, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatal("native migration function did not become live")
	}
	return dep
}

func (f *nativeDestinationFixture) get(t *testing.T, ctx context.Context, path string, want int) nativeSQLReply {
	t.Helper()
	body, _, status := nativeSQLHTTP(ctx, t, f.h.HTTPClient(), gatewayAppURL(f.h, f.slug)+path, f.slug+"."+f.config.AppDomain)
	var reply nativeSQLReply
	if status != want || json.Unmarshal(body, &reply) != nil || reply.PID <= 0 {
		t.Fatal("native migration function returned an invalid HTTP reply")
	}
	return reply
}
