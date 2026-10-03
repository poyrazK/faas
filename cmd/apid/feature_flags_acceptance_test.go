//go:build !no_pg

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type flagsAcceptanceBackend struct {
	*tenantIngressBackend
	deploymentID, instanceID string
}

func (b *flagsAcceptanceBackend) Pick(string) gateway.PickResult {
	return gateway.PickResult{OK: true, Target: gateway.Target{NodeID: b.address, DeploymentID: b.deploymentID, InstanceID: b.instanceID}}
}

// The real SDK, API, gateway and Postgres run together. Advancing the SDK clock
// exercises freshness after inactivity; this is not native KVM park/restore evidence.
func TestFeatureFlagsThreeCustomerAcceptance(t *testing.T) {
	if os.Getenv("GREGALE_FLAGS_ACCEPTANCE") != "1" {
		t.Skip("run make test-flags with DATABASE_URL")
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("Flags acceptance requires PostgreSQL")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := filepath.Abs("../../sdk/node/dist/flags.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(sdk); err != nil {
		t.Fatal("build sdk/node before Flags acceptance")
	}
	e := setupPGHandler(t, api.PlanPro)
	e.s.featureFlagsEnabled = true
	ctx := context.Background()
	slug := "flags-acceptance-api"
	project, err := e.store.CreateProject(ctx, state.Project{AccountID: e.acct.ID, Slug: "flags-acceptance", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: slug, Status: state.AppActive, ConsumerAuthMode: api.ConsumerAuthModeRequired, PlatformTenantRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production"})
	if err != nil {
		t.Fatal(err)
	}
	nodeRow, err := e.store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := e.store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, nodeRow.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var customers, keys []string
	for _, name := range []string{"one", "two", "three", "four"} {
		tenant, _, err := e.store.(state.PlatformTenantStore).CreatePlatformTenant(ctx, e.acct.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		consumer, err := e.store.CreateAPIConsumer(ctx, e.acct.ID, app.ID, name, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.store.(state.PlatformTenantStore).LinkPlatformTenantConsumer(ctx, e.acct.ID, tenant.ID, consumer.ID); err != nil {
			t.Fatal(err)
		}
		res := e.do(t, "POST", "/v1/apps/"+slug+"/consumers/"+consumer.ID+"/keys", api.CreateConsumerKeyRequest{Name: name + "-acceptance", Scopes: []string{"read"}}, nil)
		if res.Code != 201 {
			t.Fatalf("key: %d %s", res.Code, res.Body.String())
		}
		var key api.ConsumerKeyResponse
		if err = json.Unmarshal(res.Body.Bytes(), &key); err != nil {
			t.Fatal(err)
		}
		customers = append(customers, tenant.ID)
		keys = append(keys, key.Key)
	}
	path := "/v1/projects/flags-acceptance/environments/production/flags"
	update := map[string]any{"expected_version": 0, "config": map[string]any{"groups": map[string]any{}, "flags": []any{map[string]any{"key": "export", "enabled": true, "default": false, "rules": []any{map[string]any{"id": "selected", "customers": customers[:3], "value": true}}}}}}
	if res := e.do(t, "PUT", path, update, nil); res.Code != 200 {
		t.Fatalf("publish: %d %s", res.Code, res.Body.String())
	}
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(private, workloadidentity.DefaultIssuer, "flags-acceptance", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	e.s.flagsWorkloadVerifier, err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	owner := httptest.NewServer(e.h)
	t.Cleanup(owner.Close)
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("audience") != workloadidentity.FlagsAudience {
			http.Error(w, "audience", 400)
			return
		}
		token, err := signer.Mint(time.Now(), e.acct.ID, app.ID, instance.ID, workloadidentity.FlagsAudience)
		if err != nil {
			http.Error(w, "identity", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(token)
	}))
	t.Cleanup(identity.Close)
	code := `import {GregaleFlags} from ` + strconv.Quote("file://"+sdk) + `;
 import http from 'node:http';
 let offset=0;
 const flags=new GregaleFlags({apiURL:'https://api.gregale.test',identityEndpoint:` + strconv.Quote(identity.URL) + `,now:()=>Date.now()+offset,
 fetch:(url,options)=>fetch(String(url).replace('https://api.gregale.test',` + strconv.Quote(owner.URL) + `),options)});
 await flags.start();
 const server=http.createServer(async(req,res)=>{
  if(req.url==='/__advance_clock'){offset+=120000;res.end('advanced');return}
  try{await flags.runRequest(req.headers,()=>{
   const d=flags.boolean('export',false);flags.used('export');
   res.setHeader('X-Faas-Flag-Evidence',flags.responseEvidence());res.statusCode=d.value?503:200;res.end(JSON.stringify(d));
  })}catch(err){res.statusCode=500;res.end('sdk_error')}
 });server.listen(0,'127.0.0.1',()=>console.log('127.0.0.1:'+server.address().port));`
	childCtx, cancel := context.WithTimeout(ctx, time.Minute)
	cmd := exec.CommandContext(childCtx, node, "--input-type=module", "-e", code)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
	}()
	var address string
	select {
	case address = <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("Node Flags startup timed out")
	}
	if !strings.HasPrefix(address, "127.0.0.1:") {
		t.Fatal("Node Flags startup failed")
	}
	backend := &flagsAcceptanceBackend{tenantIngressBackend: &tenantIngressBackend{store: e.store, slug: slug, address: address}, deploymentID: dep.ID, instanceID: instance.ID}
	edge := gateway.NewHandlerWith(backend, gateway.NewMetrics(), nil).WithConsumerAuth(tenantIngressConsumerStore{e.store})
	recorder := gateway.NewRequestTelemetryRecorder(gateway.RequestTelemetryConfig{Enabled: true, RingSize: 32}, e.s.log)
	edge.WithRequestTelemetryRecorder(recorder)
	receiver := newRequestTelemetryReceiver(e.store.(requestTelemetryStore), e.s.ops, nil, true)
	request := func(i int, want bool, version int64) {
		r := httptest.NewRequest("GET", "http://"+slug+".gregale.dev/exports", nil)
		r.Header.Set("Authorization", "Bearer "+keys[i])
		r.Header.Set(api.PlatformTenantIDHeader, customers[(i+1)%4])
		w := httptest.NewRecorder()
		edge.ServeHTTP(w, r)
		var d struct {
			Value   bool  `json:"value"`
			Version int64 `json:"config_version"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &d); err != nil || d.Value != want || d.Version != version {
			t.Fatalf("customer %d: %d %s", i, w.Code, w.Body.String())
		}
		if w.Header().Get(api.FlagEvidenceHeader) != "" {
			t.Fatal("evidence leaked to client")
		}
		rows := recorder.DrainBatch(1)
		if len(rows) != 1 || rows[0].PlatformTenantID != customers[i] {
			t.Fatal("tenant attribution lost")
		}
		row := rows[0]
		out := receiver.handleOne(ctx, &apidpb.IncrementRequestTelemetryRequest{EventId: row.EventID.String(), AccountId: row.AccountID.String(), AppId: row.AppID.String(), DeploymentId: row.DeploymentID.String(), RouteTemplate: row.Route, Method: row.Method, HttpStatus: int32(row.Status), LatencyMs: int32(row.LatencyMS), ReceivedAtUnixMs: row.ReceivedAt.UnixMilli(), Count: 1, ConsumerId: row.ConsumerID, PlatformTenantId: row.PlatformTenantID, FlagEvidenceJson: row.FlagEvidenceJSON, InstanceId: row.InstanceID})
		if out.Outcome != rtOutcomeInserted {
			t.Fatalf("telemetry outcome=%s", out.Outcome)
		}
	}
	for i := range customers {
		request(i, i < 3, 1)
	}
	res := e.do(t, "GET", path+"/export/requests?value=true&used=true", nil, nil)
	if res.Code != 200 {
		t.Fatalf("evidence: %d %s", res.Code, res.Body.String())
	}
	var page featureFlagEvidencePage
	if err = json.Unmarshal(res.Body.Bytes(), &page); err != nil || len(page.Items) != 3 {
		t.Fatalf("evidence: %s", res.Body.String())
	}
	for _, item := range page.Items {
		if item.Status != 503 || item.LatencyMS < 0 || item.CustomerID == "" {
			t.Fatal(item)
		}
	}
	update["expected_version"] = 1
	update["config"].(map[string]any)["flags"].([]any)[0].(map[string]any)["enabled"] = false
	if res = e.do(t, "PUT", path, update, nil); res.Code != 200 {
		t.Fatalf("disable: %d %s", res.Code, res.Body.String())
	}
	// The same Node process observes disablement after inactivity, with no deploy.
	advance, err := http.Get("http://" + address + "/__advance_clock")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, advance.Body)
	_ = advance.Body.Close()
	for i := range customers {
		request(i, false, 2)
	}
	res = e.do(t, "GET", path+"/export/requests?value=false&used=true&customer_id="+customers[0], nil, nil)
	if res.Code != 200 {
		t.Fatalf("filtered evidence: %d %s", res.Code, res.Body.String())
	}
	if err = json.Unmarshal(res.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].Status != 200 {
		t.Fatalf("filtered evidence: %s", res.Body.String())
	}
	fmt.Fprintln(os.Stdout, "Flags acceptance: three targeted customers, verified attribution, error cohorts, and disablement after inactivity passed")
}
