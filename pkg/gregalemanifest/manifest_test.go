package gregalemanifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestLoad_NoManifest(t *testing.T) {
	dir := t.TempDir()
	m, ok, err := Load(dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if ok {
		t.Errorf("ok = true, want false (no manifest present)")
	}
	if m != nil {
		t.Errorf("m = %+v, want nil", m)
	}
}

func TestLoad_YAMLPresent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte("triggers: []\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	m, ok, err := Load(dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if len(m.Triggers) != 0 {
		t.Errorf("triggers = %+v, want empty", m.Triggers)
	}
}

func TestLoad_WorkflowDSL(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(`workflows:
  - name: process_order
    trigger:
      type: manual
    steps:
      - name: charge
        run: charge_stripe
        input:
          order_id: o-1
        retry:
          max_attempts: 3
          backoff: exponential
        timeout: 30s
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	m, ok, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok || len(m.Workflows) != 1 {
		t.Fatalf("loaded manifest = %+v, want one workflow", m)
	}
	wf := m.Workflows[0]
	if wf.Trigger == nil || wf.Trigger.Type != "manual" {
		t.Fatalf("trigger = %+v, want manual", wf.Trigger)
	}
	step := wf.Steps[0]
	if step.Run != "charge_stripe" || string(step.Input) != `{"order_id":"o-1"}` {
		t.Fatalf("step = %+v, input = %s", step, step.Input)
	}
	if step.Timeout != 30*time.Second {
		t.Fatalf("timeout = %v, want 30s", step.Timeout)
	}
	if err := m.ValidateForPlan(api.PlanHobby); err != nil {
		t.Fatalf("ValidateForPlan: %v", err)
	}
}

func TestValidateForPlan_WorkflowsArePaidOnly(t *testing.T) {
	m := &Manifest{Workflows: []api.WorkflowSpec{{
		Name:  "free-workflow",
		Steps: []api.WorkflowStepSpec{{Name: "main", Run: "do_work"}},
	}}}
	if err := m.ValidateForPlan(api.PlanFree); err == nil || !strings.Contains(err.Error(), "does not allow workflows") {
		t.Fatalf("error = %v, want paid-only workflow error", err)
	}
}

func TestLoad_YMLFallback(t *testing.T) {
	// .yml is the alternate extension. Both .yaml and .yml are
	// accepted; .yaml wins when both are present (load order).
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yml"), []byte("triggers: []\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, ok, err := Load(dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !ok {
		t.Errorf("ok = false, want true (yml is a valid fallback)")
	}
}

func TestLoad_TOMLRejectedExplicitly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.toml"), []byte("[triggers]\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err := Load(dir)
	if err == nil {
		t.Fatal("err = nil, want explicit TOML rejection")
	}
	if !strings.Contains(err.Error(), "TOML manifests are not supported") {
		t.Errorf("err = %q, want TOML rejection copy", err)
	}
}

func TestLoad_StrictUnknownField(t *testing.T) {
	// Typo'd `trigger:` (singular) under root. With KnownFields(true)
	// the decoder rejects the unknown top-level key, surfacing the
	// typo at load time instead of silently shipping a no-op.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"),
		[]byte("trigger:\n  - kind: cron\n    app: my-api\n    schedule: \"0 3 * * *\"\n    path: /cleanup\n"),
		0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err := Load(dir)
	if err == nil {
		t.Fatal("err = nil, want strict-decode error on unknown field")
	}
	if !strings.Contains(err.Error(), "field trigger not found") {
		t.Errorf("err = %q, want strict-decode message", err)
	}
}

func TestValidate_HappyPath(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{
		{Kind: TriggerKindCron, App: "my-api", Schedule: "0 3 * * *", Path: "/cleanup"},
		{Kind: TriggerKindCron, App: "my-api", Schedule: "*/5 * * * *", Path: "/tick", Enabled: ptrBool(false)},
	}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestValidate_UnknownKind(t *testing.T) {
	// "rabbitmq" is a kind we genuinely don't support — neither in
	// the cron-PR-C vocabulary nor in the ADR-0NN six-value widening.
	// (ADR-0NN widens to cron/kafka/nats/redis_streams/sqs_compat/queue,
	// so "queue" is now a valid kind and would not exercise the
	// unknown-kind branch — pinning "rabbitmq" here keeps the
	// pre-widening test stable through the rename.)
	m := &Manifest{Triggers: []Trigger{
		{Kind: "rabbitmq", App: "x", Schedule: "0 3 * * *", Path: "/y"},
	}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "unsupported trigger kind \"rabbitmq\"") {
		t.Errorf("err = %v, want unsupported-kind message", err)
	}
}

func TestValidate_MissingKind(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{
		{App: "x", Schedule: "0 3 * * *", Path: "/y"},
	}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "missing kind") {
		t.Errorf("err = %v, want missing-kind message", err)
	}
}

func TestValidate_BadSchedule(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{
		{Kind: TriggerKindCron, App: "x", Schedule: "not a cron", Path: "/y"},
	}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "bad schedule") {
		t.Errorf("err = %v, want bad-schedule message", err)
	}
}

func TestValidate_PathNoSlash(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{
		{Kind: TriggerKindCron, App: "x", Schedule: "0 3 * * *", Path: "cleanup"},
	}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "path must start with '/'") {
		t.Errorf("err = %v, want path-must-start-with-slash message", err)
	}
}

func TestValidate_MissingApp(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{
		{Kind: TriggerKindCron, Schedule: "0 3 * * *", Path: "/y"},
	}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "app is required") {
		t.Errorf("err = %v, want app-required message", err)
	}
}

func TestValidate_DuplicateTriple(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{
		{Kind: TriggerKindCron, App: "x", Schedule: "0 3 * * *", Path: "/y"},
		{Kind: TriggerKindCron, App: "x", Schedule: "0 3 * * *", Path: "/y"},
	}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("err = %v, want duplicate-triple message", err)
	}
}

func TestIsEnabled(t *testing.T) {
	cases := []struct {
		name string
		t    Trigger
		want bool
	}{
		{"nil pointer defaults to true", Trigger{}, true},
		{"explicit true", Trigger{Enabled: ptrBool(true)}, true},
		{"explicit false", Trigger{Enabled: ptrBool(false)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.t.IsEnabled(); got != c.want {
				t.Errorf("IsEnabled() = %v, want %v", got, c.want)
			}
		})
	}
}

// ptrBool returns a pointer to b — the YAML decoder distinguishes
// nil (absent) from explicit values via the pointer.
func ptrBool(b bool) *bool { return &b }

// Issue #757 / ADR-0NN: the manifest widens from kind=cron-only to a
// six-value closed vocabulary (cron, kafka, nats, redis_streams,
// sqs_compat, queue). The following table-driven fixtures pin the
// happy + sad paths for each of the five new kinds. The cron kind
// retains the existing TestValidate_* coverage above (no shape change).

// kafkaConfigs is the canonical happy-path config for kind=kafka.
var kafkaConfig = map[string]any{
	"brokers": []any{"broker1:9092", "broker2:9092"},
	"topic":   "orders.v1",
	"group":   "faas-orders",
}

func TestValidate_Kafka_Happy(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: kafkaConfig,
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestValidate_Kafka_MissingBrokers(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: map[string]any{"topic": "orders.v1", "group": "faas-orders"},
	}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "non-empty brokers") {
		t.Errorf("err = %v, want non-empty brokers message", err)
	}
}

func TestValidate_Kafka_MissingTopic(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: map[string]any{"brokers": []any{"b:9092"}, "group": "faas-orders"},
	}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "non-empty topic") {
		t.Errorf("err = %v, want non-empty topic message", err)
	}
}

func TestValidate_NATS_Happy(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindNATS, App: "my-api", Slug: "telemetry",
		Config: map[string]any{
			"url":     "nats://nats:4222",
			"stream":  "events",
			"subject": "events.>",
			"durable": "faas-telemetry",
		},
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestValidate_NATS_BadScheme(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindNATS, App: "my-api", Slug: "telemetry",
		Config: map[string]any{
			"url":     "http://nats:4222",
			"stream":  "events",
			"subject": "events.>",
			"durable": "faas-telemetry",
		},
	}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "nats:// or tls://") {
		t.Errorf("err = %v, want bad-scheme message", err)
	}
}

func TestValidate_RedisStreams_Happy(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindRedisStreams, App: "my-api", Slug: "cache-invalids",
		Config: map[string]any{
			"addr":   "redis:6379",
			"stream": "cacheinvalids",
			"group":  "faas-cache",
		},
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestValidate_SQSCompat_Happy(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindSQSCompat, App: "my-api", Slug: "ext-jobs",
		Config: map[string]any{
			"queue_url":      "http://faas-queue:9090/queues/ext-jobs",
			"long_poll_secs": 20,
		},
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestValidate_SQSCompat_BadLongPoll(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindSQSCompat, App: "my-api", Slug: "ext-jobs",
		Config: map[string]any{
			"queue_url":      "http://faas-queue:9090/queues/ext-jobs",
			"long_poll_secs": 25,
		},
	}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "long_poll_secs") {
		t.Errorf("err = %v, want long_poll_secs range message", err)
	}
}

func TestValidate_Queue_Happy(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindQueue, App: "my-api", Slug: "in-platform",
		Config: map[string]any{"mode": "queue"},
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestValidate_Queue_BadMode(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindQueue, App: "my-api", Slug: "in-platform",
		Config: map[string]any{"mode": "rabbit"},
	}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Errorf("err = %v, want mode message", err)
	}
}

// Non-cron kinds must carry an explicit slug; cron derives its
// identity from (schedule, path) and continues to omit slug.
func TestValidate_NonCron_RequiresSlug(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api",
		Config: kafkaConfig,
	}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "slug is required") {
		t.Errorf("err = %v, want slug-required message", err)
	}
}

// Slug must be DNS-safe — lowercase + digits + dashes, starts with a
// letter. Caps / spaces / punctuation surface at `gregale deploy`
// rather than at apid-request time.
func TestValidate_SlugNotDNSSafe(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "Bad_Slug!",
		Config: kafkaConfig,
	}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Errorf("err = %v, want DNS-safe-slug message", err)
	}
}

// Batch/window/attempts must respect the SQL CHECK range. A Hobby
// customer setting batch_size_max=500 should not get a "range" error
// here (that's per-plan and enforced server-side); the manifest
// validator only enforces the SQL CHECK envelope.
func TestValidate_BatchSizeOutOfRange(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: kafkaConfig, BatchSizeMax: 6000,
	}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "batch_size_max") {
		t.Errorf("err = %v, want batch_size_max range message", err)
	}
}

func TestValidate_MaxAttemptsOutOfRange(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: kafkaConfig, MaxAttempts: 30,
	}}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "max_attempts") {
		t.Errorf("err = %v, want max_attempts range message", err)
	}
}

// Happy-path mixed manifest: cron + kafka + nats + redis_streams +
// sqs_compat + queue. The combined Validate() runs every kind's
// validator in a single pass; the cron path keeps its (schedule,
// path) shape, the five new kinds carry (slug, config).
func TestValidate_AllSixKinds(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{
		{Kind: TriggerKindCron, App: "my-api", Schedule: "*/5 * * * *", Path: "/tick"},
		{Kind: TriggerKindKafka, App: "my-api", Slug: "orders", Config: kafkaConfig},
		{Kind: TriggerKindNATS, App: "my-api", Slug: "telemetry", Config: map[string]any{
			"url": "nats://nats:4222", "stream": "events", "subject": "events.>", "durable": "faas",
		}},
		{Kind: TriggerKindRedisStreams, App: "my-api", Slug: "cache", Config: map[string]any{
			"addr": "redis:6379", "stream": "s", "group": "g",
		}},
		{Kind: TriggerKindSQSCompat, App: "my-api", Slug: "ext", Config: map[string]any{
			"queue_url": "http://q:9090/queues/ext",
		}},
		{Kind: TriggerKindQueue, App: "my-api", Slug: "in", Config: map[string]any{"mode": "queue"}},
	}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

// Cron kind does NOT require a slug (its dedupe key is (app,
// schedule, path)) — pinning the backward-compat shape.
func TestValidate_Cron_NoSlug(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindCron, App: "my-api", Schedule: "0 3 * * *", Path: "/cleanup",
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil (cron path stays slug-free)", err)
	}
}

// ADR-118 / issue #757 §criterion 2 — TLS + SASL sub-objects on
// KafkaConfig. The validator surfaces the half-wired mTLS pair,
// the closed-vocab SASL mechanism, and the closed-vocab FilterOp
// at `gregale deploy` time rather than at the first broker dial.

// kafkaConfigWithTLS is the canonical happy-path config with TLS
// (system CA, no client cert, no skip-verify). Mirrors the
// managed-Kafka plaintext-on-public-TLS production shape.
var kafkaConfigWithTLS = map[string]any{
	"brokers": []any{"broker1:9092"},
	"topic":   "orders.v1",
	"group":   "faas-orders",
	"tls": map[string]any{
		"ca_cert": "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
	},
}

func TestValidate_Kafka_TLS_Happy(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: kafkaConfigWithTLS,
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil (TLS with system CA is the default-on production shape)", err)
	}
}

func TestValidate_Kafka_TLS_MTLSPair(t *testing.T) {
	// mTLS with both cert + key round-trips. The two fields must
	// land together; the half-wired case is the next test.
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: map[string]any{
			"brokers": []any{"broker1:9092"},
			"topic":   "orders.v1",
			"group":   "faas-orders",
			"tls": map[string]any{
				"client_cert": "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
				"client_key":  "-----BEGIN PRIVATE KEY-----\nMIIE...\n-----END PRIVATE KEY-----",
			},
		},
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil (mTLS pair is the documented production shape for Confluent Cloud cluster-scoped cert auth)", err)
	}
}

func TestValidate_Kafka_TLS_HalfWiredMTLS(t *testing.T) {
	// client_cert without client_key: a 0-byte handshake that
	// hangs at the first FetchMessage with no error. The
	// validator surfaces the typo at load time.
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: map[string]any{
			"brokers": []any{"broker1:9092"},
			"topic":   "orders.v1",
			"group":   "faas-orders",
			"tls": map[string]any{
				"client_cert": "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
			},
		},
	}}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "client_cert and client_key") {
		t.Errorf("err = %v, want half-wired-mtls message", err)
	}
}

func TestValidate_Kafka_SASL_PlainHappy(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: map[string]any{
			"brokers": []any{"broker1:9092"},
			"topic":   "orders.v1",
			"group":   "faas-orders",
			"sasl": map[string]any{
				"mechanism": "PLAIN",
				"username":  "faas-svc",
				"password":  "redacted-at-rest",
			},
		},
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil (PLAIN is the canonical SASL mechanism for managed Kafka)", err)
	}
}

func TestValidate_Kafka_SASL_Scram256Happy(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: map[string]any{
			"brokers": []any{"broker1:9092"},
			"topic":   "orders.v1",
			"group":   "faas-orders",
			"sasl": map[string]any{
				"mechanism": "SCRAM-SHA-256",
				"username":  "faas-svc",
				"password":  "redacted-at-rest",
			},
		},
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestValidate_Kafka_SASL_BadMechanism(t *testing.T) {
	// "SCRAM-SHA256" (missing dash) is the most common typo
	// against the closed vocab. Silently accepted by the YAML
	// decoder; the validator surfaces the typo at load time.
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: map[string]any{
			"brokers": []any{"broker1:9092"},
			"topic":   "orders.v1",
			"group":   "faas-orders",
			"sasl": map[string]any{
				"mechanism": "SCRAM-SHA256",
				"username":  "faas-svc",
				"password":  "redacted-at-rest",
			},
		},
	}}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "sasl mechanism") {
		t.Errorf("err = %v, want sasl-mechanism message", err)
	}
}

func TestValidate_Kafka_SASL_MissingPassword(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: map[string]any{
			"brokers": []any{"broker1:9092"},
			"topic":   "orders.v1",
			"group":   "faas-orders",
			"sasl": map[string]any{
				"mechanism": "PLAIN",
				"username":  "faas-svc",
			},
		},
	}}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "non-empty password") {
		t.Errorf("err = %v, want sasl-password message", err)
	}
}

// ADR-118 / issue #757 §criterion 4 — FilterCriteria tree.

func TestValidate_FilterCriteria_Canonical(t *testing.T) {
	// The canonical happy-path: $or / $and / payload all set
	// with the closed-vocab ops. Pins the schema that
	// pkg/sched/filter.go (commit 5) will evaluate.
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: kafkaConfig,
		FilterCriteria: &FilterCriteria{
			OR: []FilterClause{
				{Op: FilterOpExists, Field: "x-event-id"},
			},
			AND: []FilterClause{
				{Op: FilterOpEq, Field: "x-tenant", Value: jsonRaw(`"acme"`)},
			},
			Payload: []FilterClause{
				{Op: FilterOpJsonPath, Path: "$.event.type", Value: jsonRaw(`"order.created"`)},
			},
		},
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestValidate_FilterCriteria_EmptyTreeRejected(t *testing.T) {
	// A zero-Clauses tree is match-anything at runtime but a
	// degenerate config at load time. Reject so the customer
	// sees the typo at `gregale deploy`.
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config:         kafkaConfig,
		FilterCriteria: &FilterCriteria{},
	}}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "at least one of $or, $and, payload") {
		t.Errorf("err = %v, want empty-tree message", err)
	}
}

func TestValidate_FilterCriteria_UnknownOp(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: kafkaConfig,
		FilterCriteria: &FilterCriteria{
			OR: []FilterClause{
				{Op: "regex", Field: "x-event-id"},
			},
		},
	}}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "not in {eq, neq, exists, jsonpath}") {
		t.Errorf("err = %v, want unknown-op message", err)
	}
}

func TestValidate_FilterCriteria_JsonPathMissingPath(t *testing.T) {
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: kafkaConfig,
		FilterCriteria: &FilterCriteria{
			Payload: []FilterClause{
				{Op: FilterOpJsonPath, Value: jsonRaw(`"order.created"`)},
			},
		},
	}}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "non-empty path") {
		t.Errorf("err = %v, want missing-path message", err)
	}
}

func TestValidate_FilterCriteria_JsonPathShapeRejected(t *testing.T) {
	// Dot-then-bracket typo: "$.foo.[0]" is the most common
	// customer error against the strict JSONPath grammar. The
	// shape pre-check catches it at load time so the runtime
	// (commit 5) never sees it.
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: kafkaConfig,
		FilterCriteria: &FilterCriteria{
			Payload: []FilterClause{
				{Op: FilterOpJsonPath, Path: "$.foo.[0]"},
			},
		},
	}}}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "jsonpath") {
		t.Errorf("err = %v, want jsonpath shape message", err)
	}
}

func TestValidate_FilterCriteria_NestedOrAnd(t *testing.T) {
	// Nested $or inside $and: the validator recurses and
	// accepts the well-formed tree. The runtime evaluator
	// (commit 5) preserves the nesting semantics.
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindKafka, App: "my-api", Slug: "orders",
		Config: kafkaConfig,
		FilterCriteria: &FilterCriteria{
			AND: []FilterClause{
				{
					Clauses: []FilterClause{
						{Op: FilterOpEq, Field: "x-tenant", Value: jsonRaw(`"acme"`)},
						{Op: FilterOpExists, Field: "x-event-id"},
					},
				},
			},
		},
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil (nested $or/$and is the documented superset shape)", err)
	}
}

func TestValidate_FilterCriteria_CronKindSkips(t *testing.T) {
	// FilterCriteria is meaningless for kind=cron (cron always
	// fires; no broker record to filter). The validator does
	// not run validateFilterCriteria for cron — a stray
	// filter_criteria on a cron trigger is silently ignored
	// (the runtime path doesn't read it for cron). Pin the
	// backward-compat contract: a cron trigger with a stray
	// filter_criteria still validates.
	m := &Manifest{Triggers: []Trigger{{
		Kind: TriggerKindCron, App: "my-api", Schedule: "0 3 * * *", Path: "/cleanup",
		FilterCriteria: &FilterCriteria{
			OR: []FilterClause{
				{Op: FilterOpExists, Field: "x-tenant"},
			},
		},
	}}}
	if err := m.Validate(); err != nil {
		t.Errorf("err = %v, want nil (cron ignores filter_criteria; pin the backward-compat contract)", err)
	}
}

// jsonRaw is a tiny helper that returns a json.RawMessage from a
// literal. Keeps the table-driven fixtures readable.
func jsonRaw(s string) json.RawMessage { return json.RawMessage(s) }
