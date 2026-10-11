package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// fakeFunctionSetClient records the reconcile calls the fan-out makes.
type fakeFunctionSetClient struct {
	apps          map[string]api.AppResponse
	listApps      []api.AppResponse
	crons         map[string][]api.CronResponse
	buckets       map[string][]api.ObjectBucket
	notifications map[string][]api.ObjectNotificationRule // key: owner/bucket
	endpoints     map[string][]api.InboundWebhookEndpointResponse
	puts          []string
	created       []api.CreateInboundWebhookEndpointRequest
	updatedPaths  []string
	idleUpdates   map[string]int
	nextID        int
}

func newFakeFunctionSetClient() *fakeFunctionSetClient {
	return &fakeFunctionSetClient{
		apps:          map[string]api.AppResponse{},
		crons:         map[string][]api.CronResponse{},
		buckets:       map[string][]api.ObjectBucket{},
		notifications: map[string][]api.ObjectNotificationRule{},
		endpoints:     map[string][]api.InboundWebhookEndpointResponse{},
		idleUpdates:   map[string]int{},
	}
}

func (f *fakeFunctionSetClient) id() string {
	f.nextID++
	return "id-" + string(rune('a'+f.nextID))
}

func (f *fakeFunctionSetClient) Whoami(context.Context) (api.AccountResponse, error) {
	return api.AccountResponse{ID: "11111111-1111-4111-8111-111111111111", Plan: string(api.PlanPro)}, nil
}
func (f *fakeFunctionSetClient) ListCrons(_ context.Context, slug string) ([]api.CronResponse, error) {
	return f.crons[slug], nil
}
func (f *fakeFunctionSetClient) CreateCron(_ context.Context, slug string, req api.CreateCronRequest) (api.CronResponse, error) {
	cron := api.CronResponse{ID: f.id(), AppID: slug, Schedule: req.Schedule, Path: req.Path}
	f.crons[slug] = append(f.crons[slug], cron)
	return cron, nil
}
func (f *fakeFunctionSetClient) UpdateCron(_ context.Context, id string, _ api.UpdateCronRequest) (api.CronResponse, error) {
	return api.CronResponse{ID: id}, nil
}
func (f *fakeFunctionSetClient) DeleteCron(_ context.Context, id string) error {
	for slug, crons := range f.crons {
		for i, cron := range crons {
			if cron.ID == id {
				f.crons[slug] = append(crons[:i:i], crons[i+1:]...)
				return nil
			}
		}
	}
	return errors.New("no cron")
}
func (f *fakeFunctionSetClient) GetApp(_ context.Context, slug string) (api.AppResponse, error) {
	app, ok := f.apps[slug]
	if !ok {
		return app, errors.New("no app " + slug)
	}
	return app, nil
}
func (f *fakeFunctionSetClient) UpdateApp(_ context.Context, slug string, req api.UpdateAppRequest) (api.AppResponse, error) {
	if req.IdleTimeoutS != nil {
		f.idleUpdates[slug] = *req.IdleTimeoutS
	}
	return f.apps[slug], nil
}
func (f *fakeFunctionSetClient) ListApps(context.Context) ([]api.AppResponse, error) {
	return f.listApps, nil
}
func (f *fakeFunctionSetClient) ListObjectBuckets(_ context.Context, slug string) (api.ObjectBucketList, error) {
	return api.ObjectBucketList{Items: f.buckets[slug]}, nil
}
func (f *fakeFunctionSetClient) GetObjectBucketNotifications(_ context.Context, slug, bucket string) (api.ObjectBucketNotifications, error) {
	return api.ObjectBucketNotifications{BucketID: bucket, Rules: f.notifications[slug+"/"+bucket]}, nil
}
func (f *fakeFunctionSetClient) PutObjectBucketNotifications(_ context.Context, slug, bucket string, in api.ObjectBucketNotificationsRequest) (api.ObjectBucketNotifications, error) {
	f.puts = append(f.puts, slug+"/"+bucket)
	f.notifications[slug+"/"+bucket] = in.Rules
	return api.ObjectBucketNotifications{BucketID: bucket, Rules: in.Rules}, nil
}
func (f *fakeFunctionSetClient) ListInboundWebhookEndpoints(_ context.Context, slug string) ([]api.InboundWebhookEndpointResponse, error) {
	return f.endpoints[slug], nil
}
func (f *fakeFunctionSetClient) CreateInboundWebhookEndpoint(_ context.Context, slug string, req api.CreateInboundWebhookEndpointRequest) (api.InboundWebhookEndpointResponse, error) {
	f.created = append(f.created, req)
	endpoint := api.InboundWebhookEndpointResponse{ID: f.id(), Name: req.Name, Provider: req.Provider, DeliveryPath: req.DeliveryPath, EndpointURL: "https://api.gregale.dev/v1/hooks/gwh_" + req.Name}
	f.endpoints[slug] = append(f.endpoints[slug], endpoint)
	return endpoint, nil
}
func (f *fakeFunctionSetClient) UpdateInboundWebhookEndpoint(_ context.Context, slug, id string, req api.UpdateInboundWebhookEndpointRequest) (api.InboundWebhookEndpointResponse, error) {
	f.updatedPaths = append(f.updatedPaths, slug+":"+id+":"+*req.DeliveryPath)
	return api.InboundWebhookEndpointResponse{ID: id}, nil
}

const testFunctionAppID = "22222222-2222-4222-8222-222222222222"

func argValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func writeFunctionProject(t *testing.T, manifest string, roots ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, root := range roots {
		if err := os.MkdirAll(filepath.Join(dir, root), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, root, "handler.js"), []byte("export default async function handler() { return {} }\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestStripDeployFlags(t *testing.T) {
	got := stripDeployFlags([]string{"--path", "proj", "--yes", "-name=shop", "--env", "A=1", "--runtime", "node24", "--handler=h"}, "path", "name", "runtime", "handler")
	if want := []string{"--yes", "--env", "A=1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stripDeployFlags = %v, want %v", got, want)
	}
}

func TestFunctionDeployArgs(t *testing.T) {
	got := functionDeployArgs([]string{"--path", "shop", "--yes"}, plannedFunction{root: "shop/functions/thumbs", slug: "shop-thumbs", runtime: "node24", handler: "handler.handler"})
	want := []string{"--yes", "--path", "shop/functions/thumbs", "--name", "shop-thumbs", "--function", "--runtime", "node24", "--handler", "handler.handler", "--no-triggers"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("functionDeployArgs = %v, want %v", got, want)
	}
}

func TestFunctionSetPlan(t *testing.T) {
	dir := writeFunctionProject(t, "functions:\n  - name: thumbs\n    root: functions/thumbs\n  - name: digest\n    root: functions/digest\n    runtime: python313\n", "functions/thumbs", "functions/digest")
	m, ok, err := functionSetManifest(dir)
	if err != nil || !ok {
		t.Fatalf("functionSetManifest ok=%v err=%v", ok, err)
	}
	planned, err := functionSetDeploy{cwd: filepath.Dir(dir), projectDir: dir, projectSlug: "shop", manifest: m}.plan()
	if err != nil {
		t.Fatal(err)
	}
	if len(planned) != 2 || planned[0].slug != "shop-thumbs" || planned[1].slug != "shop-digest" {
		t.Fatalf("planned = %+v", planned)
	}
	if planned[0].runtime == "" || planned[1].runtime != "python313" {
		t.Fatalf("runtimes = %q, %q", planned[0].runtime, planned[1].runtime)
	}
	if want := filepath.Join(filepath.Base(dir), "functions", "thumbs"); planned[0].root != want {
		t.Fatalf("root = %q, want %q", planned[0].root, want)
	}
}

// ADR-976: functions sharing the project tree each upload the project root
// with their own handler file and inferred runtime.
func TestFunctionSetPlanSharedTree(t *testing.T) {
	dir := writeFunctionProject(t, "functions:\n  - name: resize\n    handler: api/resize.js\n  - name: digest\n    handler: api/digest.py\n  - name: sharp\n    runtime: node24\n    handler: ./api/sharp.mjs\n")
	for name, body := range map[string]string{"api/resize.js": "export async function handler() {}\n", "api/digest.py": "def handler(event, ctx):\n    return {}\n", "api/sharp.mjs": "export default async function handler() {}\n"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, _, err := functionSetManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := functionSetDeploy{cwd: dir, projectDir: dir, projectSlug: "shop", manifest: m}.plan()
	if err != nil {
		t.Fatal(err)
	}
	want := []plannedFunction{
		{slug: "shop-resize", root: ".", runtime: runtimeNode22, handler: "api/resize.js"},
		{slug: "shop-digest", root: ".", runtime: runtimePython312, handler: "api/digest.py"},
		{slug: "shop-sharp", root: ".", runtime: "node24", handler: "api/sharp.mjs"},
	}
	for i, w := range want {
		got := planned[i]
		if got.slug != w.slug || got.root != w.root || got.runtime != w.runtime || got.handler != w.handler {
			t.Errorf("planned[%d] = %+v, want %+v", i, got, w)
		}
	}

	missing := writeFunctionProject(t, "functions:\n  - name: resize\n    handler: api/missing.js\n")
	m, _, _ = functionSetManifest(missing)
	if _, err := (functionSetDeploy{cwd: missing, projectDir: missing, projectSlug: "shop", manifest: m}).plan(); err == nil || !strings.Contains(err.Error(), "is not a file") {
		t.Fatalf("missing handler error = %v", err)
	}
}

// A child deploy of a shared tree names its handler file; the tree has
// several handlers and no handler.* at its root.
func TestResolveDeployShapeExplicitHandlerFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"api/a.js", "api/b.js"} {
		if err := os.MkdirAll(filepath.Join(dir, "api"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("export async function handler() {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sh, rt, hnd, err := resolveDeployShape(dir, true, false, true, "node22", "api/a.js")
	if err != nil || sh != shapeFunction || rt != "node22" || hnd != "api/a.js" {
		t.Fatalf("resolveDeployShape = (%d, %q, %q, %v)", sh, rt, hnd, err)
	}
	if _, _, _, err := resolveDeployShape(dir, true, false, true, "node22", "api/missing.js"); err == nil {
		t.Fatal("a missing handler file must still fail")
	}
}

func TestFunctionSetPlanRejections(t *testing.T) {
	missing := writeFunctionProject(t, "functions:\n  - name: thumbs\n    root: functions/thumbs\n")
	m, _, _ := functionSetManifest(missing)
	if _, err := (functionSetDeploy{cwd: missing, projectDir: missing, projectSlug: "shop", manifest: m}).plan(); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("missing root error = %v", err)
	}
	long := writeFunctionProject(t, "functions:\n  - name: a-very-long-function-name\n    root: f\n    runtime: node24\n", "f")
	m, _, _ = functionSetManifest(long)
	if _, err := (functionSetDeploy{cwd: long, projectDir: long, projectSlug: "a-long-project-slug", manifest: m}).plan(); err == nil || !strings.Contains(err.Error(), "3-40") {
		t.Fatalf("long slug error = %v", err)
	}
}

func TestFunctionSetRunDeploysInOrderAndStopsAtFailure(t *testing.T) {
	dir := writeFunctionProject(t, "functions:\n  - name: a\n    root: fa\n    runtime: node24\n  - name: b\n    root: fb\n    runtime: node24\n  - name: c\n    root: fc\n    runtime: node24\n", "fa", "fb", "fc")
	m, _, _ := functionSetManifest(dir)
	var calls []string
	var out bytes.Buffer
	client := newFakeFunctionSetClient()
	for _, slug := range []string{"shop-a", "shop-b", "shop-c"} {
		client.apps[slug] = api.AppResponse{ID: testFunctionAppID, Slug: slug, Type: "function"}
	}
	d := functionSetDeploy{
		args: []string{"--yes"}, cwd: dir, projectDir: dir, projectSlug: "shop", manifest: m, out: &out,
		deployOne: func(args []string) int {
			name := argValue(args, "--name")
			calls = append(calls, name)
			if name == "shop-b" {
				return 1
			}
			return 0
		},
	}
	if code := d.run(context.Background(), client); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if want := []string{"shop-a", "shop-b"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("deploys = %v, want %v", calls, want)
	}
	for _, line := range []string{"Deployed 1 function(s): shop-a", "Failed: shop-b", "Not attempted: shop-c"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("summary missing %q:\n%s", line, out.String())
		}
	}
}

func TestFunctionSetRunReconcilesAfterEachDeploy(t *testing.T) {
	manifest := `functions:
  - name: thumbs
    root: f
    runtime: node24
    idle_timeout_s: 45
triggers:
  - kind: cron
    app: thumbs
    schedule: "0 * * * *"
    path: /
`
	dir := writeFunctionProject(t, manifest, "f")
	m, _, _ := functionSetManifest(dir)
	client := newFakeFunctionSetClient()
	client.apps["shop-thumbs"] = api.AppResponse{ID: testFunctionAppID, Slug: "shop-thumbs", Type: "function"}
	client.listApps = []api.AppResponse{{Slug: "shop-thumbs", Type: "function"}, {Slug: "shop-old", Type: "function"}, {Slug: "shop-web", Type: "app"}}
	var out bytes.Buffer
	d := functionSetDeploy{cwd: dir, projectDir: dir, projectSlug: "shop", manifest: m, out: &out, deployOne: func([]string) int { return 0 }}
	if code := d.run(context.Background(), client); code != 0 {
		t.Fatalf("exit = %d\n%s", code, out.String())
	}
	if client.idleUpdates["shop-thumbs"] != 45 {
		t.Fatalf("idle updates = %v", client.idleUpdates)
	}
	if crons := client.crons["shop-thumbs"]; len(crons) != 1 || crons[0].Schedule != "0 * * * *" {
		t.Fatalf("crons = %+v", crons)
	}
	if !strings.Contains(out.String(), "Not in gregale.yaml (kept): shop-old") || strings.Contains(out.String(), "shop-web") {
		t.Fatalf("unlisted report:\n%s", out.String())
	}

	// A planning run deploys nothing remote beyond the child previews.
	client2 := newFakeFunctionSetClient()
	plan := functionSetDeploy{cwd: dir, projectDir: dir, projectSlug: "shop", manifest: m, out: &out, planOnly: true, deployOne: func([]string) int { return 0 }}
	if code := plan.run(context.Background(), client2); code != 0 || len(client2.idleUpdates) != 0 || len(client2.crons) != 0 {
		t.Fatalf("plan-only run changed state: code=%d idle=%v crons=%v", code, client2.idleUpdates, client2.crons)
	}
}

func TestReconcileFunctionBucketEventsKeepsForeignRules(t *testing.T) {
	client := newFakeFunctionSetClient()
	client.buckets["shop-thumbs"] = []api.ObjectBucket{
		{ID: "b-uploads", Name: "uploads", Region: "eu-central", State: "ready"},
		{ID: "b-old", Name: "old", Region: "eu-central", State: "ready"},
	}
	foreign := api.ObjectNotificationRule{ID: "someone-else", Destination: "arn:gregale:sqs:eu-central:x:y/q", Events: []string{"s3:ObjectRemoved:*"}}
	client.notifications["shop-thumbs/b-uploads"] = []api.ObjectNotificationRule{foreign, {ID: "fn-shop-thumbs-stale", Events: []string{"s3:ObjectCreated:Put"}}}
	client.notifications["shop-thumbs/b-old"] = []api.ObjectNotificationRule{{ID: "fn-shop-thumbs-1", Events: []string{"s3:ObjectCreated:*"}}}
	events := []gregalemanifest.FunctionBucketEvent{{Bucket: "uploads", Events: []string{"s3:ObjectCreated:*"}, Prefix: "images/"}}
	account := "11111111-1111-4111-8111-111111111111"
	if err := reconcileFunctionBucketEvents(context.Background(), client, "shop-thumbs", testFunctionAppID, account, events); err != nil {
		t.Fatal(err)
	}
	got := client.notifications["shop-thumbs/b-uploads"]
	if len(got) != 2 || got[0].ID != "someone-else" || got[1].ID != "fn-shop-thumbs-1" || got[1].Prefix != "images/" {
		t.Fatalf("uploads rules = %+v", got)
	}
	if want := functionARN("eu-central", account, testFunctionAppID); got[1].Destination != want {
		t.Fatalf("destination = %q, want %q", got[1].Destination, want)
	}
	if rules := client.notifications["shop-thumbs/b-old"]; len(rules) != 0 {
		t.Fatalf("undeclared bucket keeps managed rules: %+v", rules)
	}

	// A second identical reconcile writes nothing.
	client.puts = nil
	if err := reconcileFunctionBucketEvents(context.Background(), client, "shop-thumbs", testFunctionAppID, account, events); err != nil {
		t.Fatal(err)
	}
	if len(client.puts) != 0 {
		t.Fatalf("idempotent reconcile wrote %v", client.puts)
	}
}

// A bucket managed through another app's grant may also be visible to the
// function; the cleanup pass must not strip the rules just written.
func TestReconcileFunctionBucketEventsThroughOwnerApp(t *testing.T) {
	client := newFakeFunctionSetClient()
	shared := api.ObjectBucket{ID: "b-shared", Name: "shared", Region: "eu-central", State: "ready"}
	client.buckets["shop-web"] = []api.ObjectBucket{shared}
	client.buckets["shop-thumbs"] = []api.ObjectBucket{shared}
	events := []gregalemanifest.FunctionBucketEvent{{Bucket: "shared", App: "shop-web", Events: []string{"s3:ObjectCreated:*"}}}
	if err := reconcileFunctionBucketEvents(context.Background(), client, "shop-thumbs", testFunctionAppID, "acct", events); err != nil {
		t.Fatal(err)
	}
	if want := []string{"shop-web/b-shared"}; !reflect.DeepEqual(client.puts, want) {
		t.Fatalf("puts = %v, want %v", client.puts, want)
	}
	if rules := client.notifications["shop-web/b-shared"]; len(rules) != 1 || rules[0].ID != "fn-shop-thumbs-1" {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestReconcileFunctionBucketEventsUnknownBucket(t *testing.T) {
	client := newFakeFunctionSetClient()
	err := reconcileFunctionBucketEvents(context.Background(), client, "shop-thumbs", testFunctionAppID, "acct",
		[]gregalemanifest.FunctionBucketEvent{{Bucket: "missing", Events: []string{"s3:ObjectCreated:*"}}})
	if err == nil || !strings.Contains(err.Error(), "gregale add bucket") {
		t.Fatalf("error = %v", err)
	}
}

func TestReconcileFunctionWebhooks(t *testing.T) {
	client := newFakeFunctionSetClient()
	client.endpoints["shop-pay"] = []api.InboundWebhookEndpointResponse{
		{ID: "e1", Name: "stripe", Provider: "stripe", DeliveryPath: "/old"},
		{ID: "e2", Name: "legacy", Provider: "generic"},
	}
	hooks := []gregalemanifest.FunctionWebhook{
		{Name: "stripe", Provider: "stripe", SigningSecretEnv: "STRIPE_SECRET", DeliveryPath: "/stripe"},
		{Name: "github", Provider: "generic", SigningSecretEnv: "GH_SECRET"},
	}
	var out bytes.Buffer
	env := map[string]string{"GH_SECRET": "s3cret"}
	if err := reconcileFunctionWebhooks(context.Background(), client, &out, "shop-pay", hooks, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	if want := []string{"shop-pay:e1:/stripe"}; !reflect.DeepEqual(client.updatedPaths, want) {
		t.Fatalf("updates = %v, want %v", client.updatedPaths, want)
	}
	if len(client.created) != 1 || client.created[0].Name != "github" || client.created[0].SigningSecret != "s3cret" {
		t.Fatalf("created = %+v", client.created)
	}
	if !strings.Contains(out.String(), "gwh_github") || !strings.Contains(out.String(), "shown only once") {
		t.Fatalf("endpoint URL not printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "not in gregale.yaml (kept): legacy") {
		t.Fatalf("undeclared endpoint not reported:\n%s", out.String())
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Fatal("signing secret leaked into output")
	}

	missing := []gregalemanifest.FunctionWebhook{{Name: "new", Provider: "generic", SigningSecretEnv: "NEW_SECRET"}}
	if err := reconcileFunctionWebhooks(context.Background(), client, &out, "shop-pay", missing, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "set NEW_SECRET") {
		t.Fatalf("missing secret error = %v", err)
	}
	changed := []gregalemanifest.FunctionWebhook{{Name: "legacy", Provider: "stripe", SigningSecretEnv: "S"}}
	if err := reconcileFunctionWebhooks(context.Background(), client, &out, "shop-pay", changed, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Fatalf("provider change error = %v", err)
	}
}
