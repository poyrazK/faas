package gregalemanifest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const validFunctionsManifest = `
schema_version: 1
functions:
  - name: thumbs
    root: functions/thumbs
    runtime: node24
    idle_timeout_s: 60
    bucket_events:
      - bucket: uploads
        events: ["s3:ObjectCreated:*"]
        prefix: images/
      - id: removed
        bucket: uploads
        events: ["s3:ObjectRemoved:*"]
        prefix: images/
    webhooks:
      - name: stripe
        provider: stripe
        signing_secret_env: STRIPE_WEBHOOK_SECRET
        delivery_path: /stripe
  - name: digest
    root: ./functions/digest/
triggers:
  - kind: cron
    app: digest
    schedule: "0 8 * * *"
    path: /
`

func TestFunctionsManifestParsesAndValidates(t *testing.T) {
	m, err := ParseBytes([]byte(validFunctionsManifest))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.ValidateForPlan(api.PlanPro); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(m.Functions) != 2 || m.Functions[0].Name != "thumbs" || len(m.Functions[0].BucketEvents) != 2 || len(m.Functions[0].Webhooks) != 1 {
		t.Fatalf("functions = %+v", m.Functions)
	}
}

func TestFunctionsManifestRejections(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		plan     api.Plan
		want     string
	}{
		{"empty list", "functions: []\n", api.PlanPro, "list at least one function"},
		{"bad name", "functions:\n  - name: Thumbs\n    root: f\n", api.PlanPro, "name \"Thumbs\""},
		{"duplicate name", "functions:\n  - name: a\n    root: f1\n  - name: a\n    root: f2\n", api.PlanPro, "duplicate function name"},
		{"missing root", "functions:\n  - name: a\n", api.PlanPro, "root: is required"},
		{"project root without handler", "functions:\n  - name: a\n    root: .\n", api.PlanPro, "set handler"},
		{"no root and no handler", "functions:\n  - name: a\n    runtime: node24\n", api.PlanPro, "root: is required"},
		{"handler with unknown extension", "functions:\n  - name: a\n    handler: api/a.rb\n", api.PlanPro, "set runtime"},
		{"handler wrong for runtime", "functions:\n  - name: a\n    runtime: python313\n    handler: api/a.js\n", api.PlanPro, "not a handler file for runtime python313"},
		{"go handler", "functions:\n  - name: a\n    runtime: go124\n    handler: cmd/a/main.go\n", api.PlanPro, "Go function is built from its own root"},
		{"escaping handler", "functions:\n  - name: a\n    handler: ../x.js\n", api.PlanPro, "inside the source root"},
		{"same handler twice", "functions:\n  - name: a\n    handler: api/x.js\n  - name: b\n    root: .\n    handler: ./api/x.js\n", api.PlanPro, "already the handler of function \"a\""},
		{"escaping root", "functions:\n  - name: a\n    root: ../x\n", api.PlanPro, "inside the project"},
		{"absolute root", "functions:\n  - name: a\n    root: /srv/x\n", api.PlanPro, "relative directory"},
		{"shared root", "functions:\n  - name: a\n    root: f\n  - name: b\n    root: ./f\n", api.PlanPro, "already the root"},
		{"bad runtime", "functions:\n  - name: a\n    root: f\n    runtime: ruby33\n", api.PlanPro, "not a function runtime"},
		{"idle below floor", "functions:\n  - name: a\n    root: f\n    idle_timeout_s: 5\n", api.PlanPro, "idle_timeout_s 5"},
		{"idle above plan ceiling", "functions:\n  - name: a\n    root: f\n    idle_timeout_s: 1000\n", api.PlanPro, "between 10 and 600"},
		{"bucket without events", "functions:\n  - name: a\n    root: f\n    bucket_events:\n      - bucket: b\n", api.PlanPro, "list at least one event"},
		{"bad event", "functions:\n  - name: a\n    root: f\n    bucket_events:\n      - bucket: b\n        events: [\"s3:ObjectMoved:*\"]\n", api.PlanPro, "bucket_events[0]"},
		{"overlap across functions", "functions:\n  - name: a\n    root: f1\n    bucket_events:\n      - bucket: b\n        events: [\"s3:ObjectCreated:*\"]\n  - name: c\n    root: f2\n    bucket_events:\n      - bucket: b\n        events: [\"s3:ObjectCreated:Put\"]\n", api.PlanPro, "overlapping"},
		{"bad provider", "functions:\n  - name: a\n    root: f\n    webhooks:\n      - name: w\n        provider: paypal\n        signing_secret_env: S\n", api.PlanPro, "provider \"paypal\""},
		{"bad secret env", "functions:\n  - name: a\n    root: f\n    webhooks:\n      - name: w\n        provider: generic\n        signing_secret_env: whsec_live\n", api.PlanPro, "signing_secret_env"},
		{"bad delivery path", "functions:\n  - name: a\n    root: f\n    webhooks:\n      - name: w\n        provider: generic\n        signing_secret_env: S\n        delivery_path: hook\n", api.PlanPro, "must start with /"},
		{"combined with function", "function:\n  runtime: node24\n  handler: handler.handler\nfunctions:\n  - name: a\n    root: f\n", api.PlanPro, "cannot be combined with function:"},
		{"combined with scaling", "scaling:\n  min_instances: 1\nfunctions:\n  - name: a\n    root: f\n", api.PlanPro, "cannot be combined with scaling:"},
		{"non-cron trigger", "functions:\n  - name: a\n    root: f\ntriggers:\n  - kind: queue\n    app: a\n    config:\n      mode: queue\n", api.PlanPro, "cron and realtime triggers only"},
		{"trigger for unknown function", "functions:\n  - name: a\n    root: f\ntriggers:\n  - kind: cron\n    app: b\n    schedule: \"* * * * *\"\n    path: /\n", api.PlanPro, "must name one of the functions"},
		// ADR-1342 per-function settings.
		// The bound comes from limits.go, which plan revisions change (ADR-1430).
		{"ram above plan", "functions:\n  - name: a\n    root: f\n    ram_mb: 4096\n", api.PlanHobby, fmt.Sprintf("ram_mb 4096 must be between 1 and %d", api.MustLimitsFor(api.PlanHobby).RAMMB)},
		{"timeout above plan", "functions:\n  - name: a\n    root: f\n    request_timeout_s: 100000\n", api.PlanPro, "request_timeout_s 100000"},
		{"negative min instances", "functions:\n  - name: a\n    root: f\n    min_instances: -1\n", api.PlanPro, "min_instances -1"},
		{"concurrency above per-VM bound", "functions:\n  - name: a\n    root: f\n    concurrency: 6\n", api.PlanHobby, "concurrency 6 must be between 1 and 5"},
		{"bad env key", "functions:\n  - name: a\n    root: f\n    env:\n      lower: x\n", api.PlanPro, "env \"lower\""},
		{"bad secret name", "functions:\n  - name: a\n    root: f\n    secrets_from_env: [\"api-key\"]\n", api.PlanPro, "secrets_from_env[0]"},
		{"secret listed twice", "functions:\n  - name: a\n    root: f\n    secrets_from_env: [K, K]\n", api.PlanPro, "listed twice"},
		{"env and secret", "functions:\n  - name: a\n    root: f\n    env:\n      K: v\n    secrets_from_env: [K]\n", api.PlanPro, "both a plain env value and a secret"},
		{"relative route", "functions:\n  - name: w\n    root: f0\n    entry: true\n  - name: a\n    root: f\n    routes: [api/*]\n", api.PlanPro, "must start with /"},
		{"templated route", "functions:\n  - name: w\n    root: f0\n    entry: true\n  - name: a\n    root: f\n    routes: [\"/users/{id}\"]\n", api.PlanPro, "not {param} templates"},
		{"bad route glob", "functions:\n  - name: w\n    root: f0\n    entry: true\n  - name: a\n    root: f\n    routes: [\"/a/[\"]\n", api.PlanPro, "not a path glob"},
		{"duplicate route", "functions:\n  - name: w\n    root: f0\n    entry: true\n  - name: a\n    root: f1\n    routes: [/x/*]\n  - name: b\n    root: f2\n    routes: [/x/*]\n", api.PlanPro, "already a route of function \"a\""},
		{"routes without entry", "functions:\n  - name: a\n    root: f\n    routes: [/a/*]\n", api.PlanPro, "set entry: true"},
		{"two entries", "functions:\n  - name: a\n    root: f1\n    entry: true\n  - name: b\n    root: f2\n    entry: true\n", api.PlanPro, "only one function can be the entry"},
		{"entry with routes", "functions:\n  - name: a\n    root: f\n    entry: true\n    routes: [/a/*]\n", api.PlanPro, "remove its routes"},
		{"pull queue binding", "functions:\n  - name: a\n    root: f\n    queue_bindings:\n      - name: jobs\n        queue_name: jobs\n        mode: pull\n", api.PlanPro, "consumes queues by push"},
		{"duplicate queue binding", "functions:\n  - name: a\n    root: f\n    queue_bindings:\n      - name: jobs\n        queue_name: jobs\n      - name: jobs\n        queue_name: other\n", api.PlanPro, "duplicate queue binding name"},
		{"bad queue name", "functions:\n  - name: a\n    root: f\n    queue_bindings:\n      - name: jobs\n        queue_name: Jobs\n", api.PlanPro, "queue_name must match"},
		{"event trigger names an app", "functions:\n  - name: a\n    root: f\n    event_triggers:\n      - app: other\n        source: billing.*\n        type: invoice.paid\n", api.PlanPro, "remove app; a function"},
		{"event trigger without source", "functions:\n  - name: a\n    root: f\n    event_triggers:\n      - type: invoice.paid\n", api.PlanPro, "functions[0]: triggers.event[0].source"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := ParseBytes([]byte(tt.manifest))
			if err == nil {
				err = m.ValidateForPlan(tt.plan)
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

// Several functions may share one source tree when each names its handler.
func TestFunctionsManifestSharedTree(t *testing.T) {
	manifest := `functions:
  - name: resize
    handler: api/resize.js
  - name: digest
    handler: api/digest.py
  - name: thumbs
    root: services/thumbs
`
	m, err := ParseBytes([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ValidateForPlan(api.PlanPro); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := m.Functions[0].FunctionRoot(); got != "." {
		t.Fatalf("handler-only function root = %q, want the project", got)
	}
	if got := m.Functions[2].FunctionRoot(); got != "services/thumbs" {
		t.Fatalf("root = %q", got)
	}
	if HandlerRuntimeFamily("api/digest.py") != "python" || HandlerRuntimeFamily("api/x.mjs") != "node" || HandlerRuntimeFamily("main.go") != "" {
		t.Fatal("HandlerRuntimeFamily mapping drifted")
	}
}

// adr: 985
func TestRepositoryDeployFunctions(t *testing.T) {
	plain := &Manifest{}
	if skip, err := plain.RepositoryDeployFunctions(false); skip || err != nil {
		t.Fatalf("no functions: skip=%v err=%v, want neither", skip, err)
	}
	fanOut := &Manifest{Functions: []FunctionSpec{{Name: "thumbs"}}}
	if skip, err := fanOut.RepositoryDeployFunctions(true); !skip || err != nil {
		t.Fatalf("function app: skip=%v err=%v, want skip", skip, err)
	}
	if skip, err := fanOut.RepositoryDeployFunctions(false); skip || err == nil {
		t.Fatalf("other app: skip=%v err=%v, want refusal", skip, err)
	}
}

func TestFunctionBucketRuleID(t *testing.T) {
	if got := FunctionBucketRuleID("shop-thumbs", 0, FunctionBucketEvent{}); got != "fn-shop-thumbs-1" {
		t.Fatalf("default id = %q", got)
	}
	if got := FunctionBucketRuleID("shop-thumbs", 3, FunctionBucketEvent{ID: "images"}); got != "fn-shop-thumbs-images" {
		t.Fatalf("explicit id = %q", got)
	}
	if !strings.HasPrefix(FunctionBucketRuleID("shop-thumbs", 0, FunctionBucketEvent{}), FunctionBucketRulePrefix("shop-thumbs")) {
		t.Fatal("rule id must carry the function prefix")
	}
}

// ADR-1342: per-function app settings and routes parse and validate.
func TestFunctionsManifestSettings(t *testing.T) {
	m, err := ParseBytes([]byte(`
functions:
  - name: web
    root: functions/web
    entry: true
  - name: users
    root: functions/users
    runtime: node24
    ram_mb: 512
    request_timeout_s: 120
    min_instances: 0
    concurrency: 16
    env:
      LOG_LEVEL: debug
    secrets_from_env: [DATABASE_URL]
    routes: ["/users/*", "/users"]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := m.ValidateForPlan(api.PlanPro); err != nil {
		t.Fatalf("validate: %v", err)
	}
	users := m.Functions[1]
	if users.RAMMB != 512 || users.RequestTimeoutS != 120 || users.MinInstances == nil || *users.MinInstances != 0 ||
		users.Concurrency != 16 || users.Env["LOG_LEVEL"] != "debug" || len(users.SecretsFromEnv) != 1 || len(users.Routes) != 2 {
		t.Fatalf("users = %+v", users)
	}
	if !m.Functions[0].Entry || m.Functions[0].HasPreDeploySettings() || !users.HasPreDeploySettings() {
		t.Fatalf("entry/pre-deploy flags wrong: %+v", m.Functions)
	}
}
