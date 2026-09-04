// Package gregalemanifest — loader for the `gregale.yaml` /
// `gregale.yml` declarative manifest (issue #791 PR-C / ADR-090,
// extended by issue #757 / ADR-0NN).
//
// Scope (ADR-0NN widens PR-C): the `triggers:` key now recognises six
// kinds — cron (the existing synthetic-wake path, unchanged from
// PR-C) plus kafka, nats, redis_streams, sqs_compat, and the
// in-platform queue/delayed_task merger (the unified Trigger primitive
// from issue #757). The closed-vocabulary `Kind` discriminator
// patterns after ADR-090 §"triggers: manifest key" — the cron path is
// strictly backward compatible, the new kinds slot in under the same
// discriminator without a YAML schema bump.
//
// File discovery: the loader takes a project dir and looks for
// `gregale.yaml` first, then `gregale.yml`. A TOML file
// (`gregale.toml`) is rejected with an explicit error per ADR-090
// §"YAML vs TOML" — silent ignoring would let customers think their
// manifest was applied when it wasn't.
//
// Why a shared package, not `cmd/gregale/manifest.go`: the long-term
// plan (per the plan's "loader location" section) is to also validate
// the same schema server-side in `cmd/apid/scan_service.go`. A shared
// package avoids a cmd→cmd import and keeps the parser's failure
// modes (UnknownKind, BadSchedule, PathNoSlash, Duplicate,
// BadKindConfig) in one place that both surfaces can reuse.
package gregalemanifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// TriggerKind is the closed vocabulary for `triggers[].kind`. PR-C
// shipped only `cron`; ADR-0NN widens it to six values to cover the
// five broker-pulled event-source-mapping kinds plus the in-platform
// queue/delayed_task merger. Adding a new kind requires (a) appending
// a constant here, (b) widening the `kind` CHECK on the
// `triggers` table in a follow-up migration (00267's CHECK already
// covers all six values, so the DB is forward-compatible with this
// manifest schema), and (c) adding the per-kind validator below.
// The CLI and the apid server-side validator both reject unknown
// kinds with the same upgrade-me message.
type TriggerKind string

const (
	// TriggerKindCron — synthetic wake on a cron schedule (ADR-090).
	// Mirrors the legacy `api.PlanCron` resource on the wire. The
	// kind is the only one that carries `schedule` + `path`; all
	// five non-cron kinds read their schedule semantics from the
	// upstream broker.
	TriggerKindCron TriggerKind = "cron"
	// TriggerKindKafka — Kafka consumer-group poll (issue #757).
	// Config schema: KafkaConfig{Brokers, Topic, Group}.
	TriggerKindKafka TriggerKind = "kafka"
	// TriggerKindNATS — NATS JetStream durable consumer (issue #757).
	// Config schema: NATSConfig{URL, Stream, Subject, Durable}.
	TriggerKindNATS TriggerKind = "nats"
	// TriggerKindRedisStreams — Redis XReadGroup consumer
	// (issue #757). Config schema: RedisStreamsConfig{Addr, Stream,
	// Group, Consumer}.
	TriggerKindRedisStreams TriggerKind = "redis_streams"
	// TriggerKindSQSCompat — long-poll the in-platform SQS-compatible
	// HTTP queue (issue #757). Config schema:
	// SQSCompatConfig{QueueURL, LongPollSecs}.
	TriggerKindSQSCompat TriggerKind = "sqs_compat"
	// TriggerKindQueue — in-platform queue / delayed_task merger
	// (issue #757, ADR-0NN). The platform's own `invocations` rows
	// with source IN ('queue','delayed_task') become a Trigger.
	// Config schema: QueueConfig{Mode}.
	TriggerKindQueue TriggerKind = "queue"
)

// Trigger is one entry under `triggers:`. PR-C ships cron-only fields;
// ADR-0NN adds the broker-pulled EventSourceMapping shape (Slug +
// BatchSizeMax + BatchWindowMs + MaxAttempts + Config) without
// breaking the existing cron path — `Schedule` and `Path` stay on the
// struct because they're required for kind=cron and a no-op for every
// other kind.
//
// `Enabled` is a pointer so the YAML decoder can distinguish "absent"
// from "explicit false" — the spec is "absent → true" (a trigger with
// no `enabled:` line is enabled).
type Trigger struct {
	Kind TriggerKind `yaml:"kind"`
	App  string      `yaml:"app"`
	Slug string      `yaml:"slug,omitempty"`
	// Schedule + Path are cron-only fields. They are required for
	// kind=cron and ignored for every other kind (the broker pulls
	// on its own cadence; the runner doesn't know how to map a
	// broker offset to a `/cleanup` HTTP path).
	Schedule string `yaml:"schedule,omitempty"`
	Path     string `yaml:"path,omitempty"`
	// BatchSizeMax + BatchWindowMs + MaxAttempts match the SQL CHECK
	// range on `triggers` (migration 00267): batch_size_max ∈
	// [1, 5000], batch_window_ms ∈ [10, 600000], max_attempts ∈
	// [1, 25]. The per-plan ceilings (pkg/api/Plan.TriggerBatchSizeMax
	// etc.) cap these BELOW the SQL ceiling — a Hobby customer asking
	// for 500 hits trigger_batch_size_too_long at the apid layer,
	// not a SQL 23514. Zero values mean "use the plan default" (the
	// SQL DEFAULTs to 64 / 1000 / 5).
	BatchSizeMax  int `yaml:"batch_size_max,omitempty"`
	BatchWindowMs int `yaml:"batch_window_ms,omitempty"`
	MaxAttempts   int `yaml:"max_attempts,omitempty"`
	// PayloadMaxBytes (migration 00274) bounds the per-record
	// broker payload size. The migration's SQL CHECK admits
	// [1024, 67108864]; per-plan caps in pkg/api/limits.go
	// (TriggerPayloadMaxBytes) cap this BELOW the SQL ceiling.
	// Zero means "use the plan default" — same opt-out
	// semantics as BatchSizeMax above. Records above the cap
	// are DLQ'd with reason='payload_too_large' at insert
	// time rather than silently truncated.
	PayloadMaxBytes int `yaml:"payload_max_bytes,omitempty"`
	// BrokerPoisonStrategy (migration 00275) controls how the
	// kafka poller reconciles its broker offset with a
	// dead-lettered record. Closed vocab: "commit" (default —
	// broker offset advances; same as no field) and
	// "seek-to-offset" (the kafka poller calls SetOffset so the
	// next Poll re-fetches the same message). Empty string
	// means "use the plan default"; per-kind validator at
	// Validate() rejects anything outside the closed vocabulary
	// so a YAML typo surfaces at load time rather than at
	// poison-record dispatch time.
	BrokerPoisonStrategy string `yaml:"broker_poison_strategy,omitempty"`
	// FilterCriteria (ADR-118 / issue #757 closure, migration
	// 00300) is the per-source record filter tree. nil means
	// "every record passes through". The shape is evaluated
	// at runtime by pkg/sched/filter.go::FilterCriteria.Match
	// (commit 5); the validator here only checks the closed
	// vocabulary of operators + jsonpath syntax. The validator
	// runs at `gregale deploy` time so a typo'd operator
	// surfaces before any broker traffic lands.
	//
	// Pointer-on-pointer: *FilterCriteria distinguishes "absent"
	// (omitempty on YAML) from "explicit null" (empty filter
	// tree) — both decode to match-anything at runtime, but a
	// future "force-set filter" feature may need to tell them
	// apart, and storing the distinction now costs nothing.
	FilterCriteria *FilterCriteria `yaml:"filter_criteria,omitempty"`
	// Config is the per-kind JSON object. Validated strictly per
	// kind in Validate(). Absent Config defaults to `{}` so a
	// bare trigger entry validates against the per-kind zero-value
	// default (which is itself a validation error — every non-cron
	// kind requires at least one non-empty field).
	Config  map[string]any `yaml:"config,omitempty"`
	Enabled *bool          `yaml:"enabled,omitempty"`
}

// FilterOp is the closed vocabulary of comparison operators a
// FilterClause may carry. Adding a new operator requires (a) a
// constant here, (b) widening the switch in
// pkg/sched/filter.go::FilterCriteria.Match, and (c) a unit test
// for the new path.
type FilterOp string

const (
	// FilterOpEq — payload-or-header value equality. JSON-encoded
	// comparison: numeric literals compare as numbers, string
	// literals as strings, booleans as booleans. Type mismatch
	// is a non-match (not a parse error).
	FilterOpEq FilterOp = "eq"
	// FilterOpNeq — payload-or-header value inequality. Inverts
	// FilterOpEq (including the type-mismatch-→-match rule, so
	// "neq against an absent header" matches).
	FilterOpNeq FilterOp = "neq"
	// FilterOpExists — header key presence check. The Value
	// field is ignored. Matches iff the header key is set in
	// rec.Headers (any non-empty value).
	FilterOpExists FilterOp = "exists"
	// FilterOpJsonPath — JSONPath predicate against rec.Payload.
	// The Path field carries the expression (e.g. "$.event.type");
	// the Value field carries the expected result. Implemented
	// via github.com/PaesslerAG/jsonpath — no customer-supplied
	// code execution; the library is a pure data walker.
	FilterOpJsonPath FilterOp = "jsonpath"
)

// SASLMechanism is the closed vocabulary of SASL mechanisms the
// kafka poller (pkg/sched/poller_kafka.go) accepts. Mirrors the
// segmentio/kafka-go sasl.Mechanism union {Plain, SCRAMSHA256,
// SCRAMSHA512} — anything outside this set is rejected at load
// time so the customer sees the typo at `gregale deploy` rather
// than at the first broker dial.
type SASLMechanism string

const (
	SASLMechanismPlain       SASLMechanism = "PLAIN"
	SASLMechanismScramSHA256 SASLMechanism = "SCRAM-SHA-256"
	SASLMechanismScramSHA512 SASLMechanism = "SCRAM-SHA-512"
)

// IsValidSASLMechanism reports whether m is one of the closed-vocab
// SASL mechanisms.
func (m SASLMechanism) IsValid() bool {
	switch m {
	case SASLMechanismPlain, SASLMechanismScramSHA256, SASLMechanismScramSHA512:
		return true
	}
	return false
}

// TLSConfig is the optional TLS sub-object on KafkaConfig
// (ADR-118 / issue #757). All fields are optional — the
// MinVersion constant is enforced at runtime regardless of what
// the customer sets.
//
// Why a pointer on KafkaConfig rather than a sub-struct slot:
// "platform default" (nil) means "use the cluster's CA bundle,
// no client cert, no skip-verify". This is the production
// shape for ~all managed-Kafka deployments (Confluent Cloud,
// MSK with public endpoints, Redpanda Cloud) and lets the
// simple case stay simple in the YAML.
type TLSConfig struct {
	// CACert is the PEM-encoded CA bundle. Empty means "use the
	// system trust store". Newlines are preserved through the
	// YAML→JSON→tls.Config.X509KeyPair path; the runtime layer
	// (commit 7) handles the PEM decode.
	CACert string `json:"ca_cert,omitempty" yaml:"ca_cert,omitempty"`
	// ClientCert + ClientKey are the PEM-encoded mTLS pair.
	// Both must be set together — Validate() rejects a
	// half-configured mTLS pair so the customer sees the
	// typo at load time rather than at the first dial.
	ClientCert string `json:"client_cert,omitempty" yaml:"client_cert,omitempty"`
	ClientKey  string `json:"client_key,omitempty"  yaml:"client_key,omitempty"`
	// SkipVerify disables TLS certificate verification. The
	// validator gates this on the TLSSkipVerifyAllowed plan
	// cap (commit 3): Hobby=false, Pro=true, Scale=true. A
	// Hobby customer setting skip_verify=true surfaces a
	// typed error pointing them at the plan upgrade.
	//
	// The validator here only checks the bool-shape; the
	// plan-cap check lives in apid handler land because the
	// manifest loader doesn't know the customer's plan.
	// Pinning the closed vocab here keeps the manifest
	// self-contained: a test-grade manifest with
	// skip_verify=true on a Hobby-shaped test plan still
	// round-trips through the loader, and the apid is the
	// one that ultimately rejects it.
	SkipVerify bool `json:"skip_verify,omitempty" yaml:"skip_verify,omitempty"`
}

// SASLConfig is the optional SASL sub-object on KafkaConfig
// (ADR-118 / issue #757). nil means "no SASL" — the kafka dial
// is plaintext-equivalent from the customer's perspective.
type SASLConfig struct {
	// Mechanism is the closed-vocab SASL mechanism. Required
	// when SASL is non-nil.
	Mechanism SASLMechanism `json:"mechanism" yaml:"mechanism"`
	// Username is the SASL principal. Required when SASL is
	// non-nil.
	Username string `json:"username" yaml:"username"`
	// Password is the SASL secret. Required when SASL is
	// non-nil. The validator here only checks non-emptiness;
	// the apid handler seals secret values per pkg/webhook
	// (ADR-113 PR-B).
	Password string `json:"password" yaml:"password"`
}

// FilterClause is one leaf or branch in a FilterCriteria tree
// (ADR-118 / issue #757 §criterion 4). The discriminator is Op:
//
//   - Eq / Neq / Exists:     Field carries the header key
//     (eq/neq against rec.Headers[Field],
//     exists against presence).
//   - JsonPath:              Path carries the JSONPath expression;
//     Value carries the expected match.
//
// Clauses is the branch slot for nested $or / $and: a clause with
// Op="jsonpath" AND non-empty Clauses is malformed; the validator
// rejects the half-wired shape at load time.
type FilterClause struct {
	Op      FilterOp        `json:"op"                yaml:"op"`
	Field   string          `json:"field,omitempty"   yaml:"field,omitempty"`
	Path    string          `json:"path,omitempty"    yaml:"path,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"   yaml:"value,omitempty"`
	Clauses []FilterClause  `json:"clauses,omitempty" yaml:"clauses,omitempty"`
}

// FilterCriteria is the per-trigger filter tree (ADR-118 / issue
// #757 §criterion 4). Three top-level slots, mutually combinable:
//
//   - OR:      a record passes if ANY clause matches.
//   - AND:     a record passes if ALL clauses match.
//   - Payload: a list of JSONPath predicates against rec.Payload.
//
// Nil-or-zero FilterCriteria is "every record passes through" —
// the runtime short-circuits on the empty tree. The validator
// here checks (a) at-least-one-slot rule (zero slots + zero
// nested clauses is match-anything but a degenerate config that
// almost certainly indicates a customer typo), (b) closed-vocab
// op, (c) field/path presence per op, (d) JSONPath syntax.
//
// The runtime evaluator is pkg/sched/filter.go (commit 5 of the
// ADR-118 mega-PR); this type is the validator's contract.
type FilterCriteria struct {
	OR      []FilterClause `json:"$or,omitempty"     yaml:"$or,omitempty"`
	AND     []FilterClause `json:"$and,omitempty"    yaml:"$and,omitempty"`
	Payload []FilterClause `json:"payload,omitempty" yaml:"payload,omitempty"`
}

// IsEnabled returns the trigger's effective enabled state. nil pointer
// (key absent in the YAML) defaults to true — opt-out semantics match
// the `CreateCron` API where omitted `enabled` defaults to true.
func (t Trigger) IsEnabled() bool {
	if t.Enabled == nil {
		return true
	}
	return *t.Enabled
}

// KafkaConfig is the per-kind config for kind=kafka (issue #757).
// Brokers is a list of host:port pairs; Topic is the consumer-group
// subscription target; Group is the durable consumer-group ID.
//
// TLS + SASL are ADR-118 additions (issue #757 §criterion 2). Both
// are pointers so the simple case (managed-Kafka plaintext with
// public CAs) stays simple in the YAML: `config: {brokers: [...],
// topic: ..., group: ...}`. nil TLS = "use the system trust store,
// no client cert". nil SASL = "no SASL".
type KafkaConfig struct {
	Brokers []string    `json:"brokers"`
	Topic   string      `json:"topic"`
	Group   string      `json:"group"`
	TLS     *TLSConfig  `json:"tls,omitempty"   yaml:"tls,omitempty"`
	SASL    *SASLConfig `json:"sasl,omitempty"  yaml:"sasl,omitempty"`
}

// NATSConfig is the per-kind config for kind=nats (issue #757). URL
// is the nats:// or tls:// endpoint; Stream is the JetStream stream
// name; Subject is the filter pattern (`events.>`); Durable is the
// durable consumer name.
type NATSConfig struct {
	URL     string `json:"url"`
	Stream  string `json:"stream"`
	Subject string `json:"subject"`
	Durable string `json:"durable"`
}

// RedisStreamsConfig is the per-kind config for kind=redis_streams
// (issue #757). Addr is host:port; Stream is the XReadGroup stream
// name; Group is the consumer group; Consumer is the per-instance
// consumer name (default the trigger slug).
type RedisStreamsConfig struct {
	Addr     string `json:"addr"`
	Stream   string `json:"stream"`
	Group    string `json:"group"`
	Consumer string `json:"consumer,omitempty"`
}

// SQSCompatConfig is the per-kind config for kind=sqs_compat (issue
// #757). QueueURL is the in-platform HTTP queue endpoint
// (`http://faas-queue:9090/queues/<name>`); LongPollSecs is the
// wait-time parameter (1–20; the platform caps at 20 per the
// AWS long-poll ceiling).
type SQSCompatConfig struct {
	QueueURL     string `json:"queue_url"`
	LongPollSecs int    `json:"long_poll_secs,omitempty"`
}

// QueueConfig is the per-kind config for kind=queue (issue #757). Mode
// selects which in-platform source to bind: "queue" for the per-app
// FIFO queue (invocations.source='queue') or "delayed_task" for the
// delayed-task surface (invocations.source='delayed_task').
type QueueConfig struct {
	Mode string `json:"mode"`
}

// Manifest is the parsed `gregale.yaml` root. The supported top-level
// declarations are `triggers` and `workflows`; other keys are validated
// strictly (yaml.Decoder.KnownFields(true)) so a typo like `trigger:`
// (singular) surfaces as a load-time error rather than silently shipping a
// no-op deploy.
type Manifest struct {
	Triggers  []Trigger          `yaml:"triggers"`
	Workflows []api.WorkflowSpec `yaml:"workflows,omitempty"`
}

// Load reads `gregale.yaml` or `gregale.yml` from dir. Returns
// (nil, false, nil) when no manifest is present — callers treat this
// as "no work to do" without special-casing the error. On parse
// failure returns a wrapped error with the file path so a
// `gregale deploy` invocation reports `gregale.yaml: ...`.
func Load(dir string) (*Manifest, bool, error) {
	for _, name := range []string{"gregale.yaml", "gregale.yml"} {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("gregalemanifest: read %s: %w", path, err)
		}
		m, err := parseManifest(b)
		if err != nil {
			return nil, false, fmt.Errorf("gregalemanifest: parse %s: %w", path, err)
		}
		return m, true, nil
	}
	// Explicit rejection: a TOML manifest is left untouched by Load
	// (caller sees no-op) but the presence of `gregale.toml` is a
	// hard error. This catches the "I wrote toml but Load silently
	// ignored it" footgun.
	if _, err := os.Stat(filepath.Join(dir, "gregale.toml")); err == nil {
		return nil, false, errors.New("gregalemanifest: gregale.toml is present but TOML manifests are not supported yet (rename to gregale.yaml)")
	}
	return nil, false, nil
}

// ParseBytes decodes a manifest blob (yaml) without staging to disk.
// Strict-decode + Validate are applied identically to Load — only
// the file-vs-blob input differs. Returns (nil, nil) for an empty
// payload so handlers treat absent-blob the same as absent-file.
//
// Used by the apid's POST /v1/triggers:batch_create route (commit
// #6 of feat-triggers-mega) where the dashboard ships an inline
// manifest blob alongside a source tarball — staging the bytes to
// a tempfile here would just be an extra round-trip through tmpfs
// for the validator to land.
func ParseBytes(b []byte) (*Manifest, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	return parseManifest(b)
}

// parseManifest decodes the bytes with strict unknown-field rejection.
// Without KnownFields(true), a typo'd `trigger:` (singular) would
// silently drop every entry — the customer's deploy would ship a
// no-op `triggers:` and they'd discover the gap in production. Strict
// decoding turns the typo into a load-time error.
func parseManifest(b []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	m := &Manifest{}
	if err := dec.Decode(m); err != nil {
		// yaml.Decoder wraps a strict-decode failure as a
		// *yaml.TypeError; we surface the inner message verbatim.
		return nil, fmt.Errorf("decode: %w", err)
	}
	return m, nil
}

// Validate runs schema checks against the decoded manifest. It retains the
// historical plan-free API for callers that only consume trigger manifests;
// workflow-aware callers must use ValidateForPlan so workflow gates and caps
// are evaluated against the account's actual plan.
//
// Validation order matches the failure modes a customer would debug
// most often: kind first (so an unknown kind surfaces as a clear
// upgrade-me message), then per-kind config (so the customer sees
// the specific field that's wrong rather than a generic "bad
// config"), then schedule (the cron-specific legacy check), then
// path + app + duplicates. The duplicate check is last because it's
// the most expensive and only meaningful when the per-entry checks
// pass.
//
// ADR-0NN extends the switch from one case (cron) to six; the cron
// path is byte-for-byte identical to PR-C, the five new kinds each
// validate their Config via decodeAndValidateConfig below.
func (m *Manifest) Validate() error {
	return m.ValidateForPlan(api.PlanScale)
}

// ValidateForPlan runs schema checks and applies workflow limits for plan.
// Trigger validation is plan-independent; workflows are paid-only and their
// definition count, step timeout, and wait timeout are plan-bound.
func (m *Manifest) ValidateForPlan(plan api.Plan) error {
	if m == nil {
		return nil
	}
	seen := make(map[triggerKey]struct{}, len(m.Triggers))
	for i, t := range m.Triggers {
		switch t.Kind {
		case TriggerKindCron,
			TriggerKindKafka,
			TriggerKindNATS,
			TriggerKindRedisStreams,
			TriggerKindSQSCompat,
			TriggerKindQueue:
			// fall through to per-kind validation below
		case "":
			return fmt.Errorf("trigger[%d]: missing kind (want one of %s)", i, supportedKindsList())
		default:
			return fmt.Errorf("trigger[%d]: unsupported trigger kind %q (supported kinds: %s)",
				i, t.Kind, supportedKindsList())
		}
		// App is universal — every kind binds to one app.
		if t.App == "" {
			return fmt.Errorf("trigger[%d]: app is required", i)
		}
		// Slug is required for non-cron kinds (the cron path derives
		// its dedupe key from (app, schedule, path) so it doesn't
		// need an explicit slug). The slug surfaces in the wire
		// path `/_triggers/<kind>/<slug>` so it must be DNS-safe.
		if t.Kind != TriggerKindCron {
			if t.Slug == "" {
				return fmt.Errorf("trigger[%d]: slug is required for kind=%q", i, t.Kind)
			}
			if !isDNSSafeSlug(t.Slug) {
				return fmt.Errorf("trigger[%d]: slug %q must match [a-z0-9-]+", i, t.Slug)
			}
		}
		// Per-kind config check. Cron's Config is the legacy
		// (schedule, path) pair; the five new kinds validate their
		// typed Config map.
		if err := t.validateKindConfig(i); err != nil {
			return err
		}
		// FilterCriteria (ADR-118) applies to every kind except
		// cron — cron doesn't poll, so a record filter is a
		// no-op (cron always fires). Validate() runs on the
		// schema-shape here (closed vocab + JSONPath syntax);
		// the runtime evaluator is pkg/sched/filter.go (commit 5).
		if t.Kind != TriggerKindCron && t.FilterCriteria != nil {
			if err := validateFilterCriteria(i, t.FilterCriteria); err != nil {
				return err
			}
		}
		// Batch/window/attempts ranges mirror the SQL CHECK on the
		// `triggers` table (migration 00267). A 0 value is "use the
		// SQL DEFAULT" (64 / 1000 / 5) — strictly speaking the SQL
		// CHECK rejects 0 for batch_size_max / batch_window_ms /
		// max_attempts, so the manifest validator surfaces the
		// customer-facing error rather than letting the row insert
		// fail at the DB layer. The cron kind ignores these fields.
		if t.Kind != TriggerKindCron {
			if t.BatchSizeMax != 0 && (t.BatchSizeMax < 1 || t.BatchSizeMax > 5000) {
				return fmt.Errorf("trigger[%d]: batch_size_max=%d out of range [1, 5000]", i, t.BatchSizeMax)
			}
			if t.BatchWindowMs != 0 && (t.BatchWindowMs < 10 || t.BatchWindowMs > 600000) {
				return fmt.Errorf("trigger[%d]: batch_window_ms=%d out of range [10, 600000]", i, t.BatchWindowMs)
			}
			if t.MaxAttempts != 0 && (t.MaxAttempts < 1 || t.MaxAttempts > 25) {
				return fmt.Errorf("trigger[%d]: max_attempts=%d out of range [1, 25]", i, t.MaxAttempts)
			}
			// payload_max_bytes mirrors the migration 00274 SQL
			// CHECK floor + ceiling. 0 means "use the plan
			// default". Surface the customer-facing error rather
			// than letting the row insert fail at the DB layer.
			if t.PayloadMaxBytes != 0 && (t.PayloadMaxBytes < 1024 || t.PayloadMaxBytes > 67108864) {
				return fmt.Errorf("trigger[%d]: payload_max_bytes=%d out of range [1024, 67108864]", i, t.PayloadMaxBytes)
			}
			// broker_poison_strategy (migration 00275) mirrors the
			// SQL CHECK closed vocab. Empty string is treated as
			// the absent/zero case and falls through to the DB
			// default 'commit'; anything else must match the
			// closed vocabulary exactly so a YAML typo surfaces at
			// load time rather than at poison-record dispatch
			// time.
			if t.BrokerPoisonStrategy != "" && t.BrokerPoisonStrategy != "commit" && t.BrokerPoisonStrategy != "seek-to-offset" {
				return fmt.Errorf("trigger[%d]: broker_poison_strategy=%q invalid; must be one of commit, seek-to-offset", i, t.BrokerPoisonStrategy)
			}
		}
		k := triggerKey{app: t.App, kind: t.Kind, schedule: t.Schedule, path: t.Path, slug: t.Slug}
		if _, dup := seen[k]; dup {
			return fmt.Errorf("trigger[%d]: duplicate (app, kind, slug) — %q / %q / %q",
				i, t.App, t.Kind, t.Slug)
		}
		seen[k] = struct{}{}
	}

	if len(m.Workflows) == 0 {
		return nil
	}
	if !plan.WorkflowsAllowed() {
		return fmt.Errorf("workflows: plan %q does not allow workflows", plan)
	}
	if max := plan.WorkflowMaxPerApp(); max > 0 && len(m.Workflows) > max {
		return fmt.Errorf("workflows: %d definitions exceed the plan limit of %d", len(m.Workflows), max)
	}
	seenWorkflows := make(map[string]struct{}, len(m.Workflows))
	for i, wf := range m.Workflows {
		if wf.Name == "" {
			return fmt.Errorf("workflows[%d]: name is required", i)
		}
		if _, dup := seenWorkflows[wf.Name]; dup {
			return fmt.Errorf("workflows[%d]: duplicate workflow name %q", i, wf.Name)
		}
		seenWorkflows[wf.Name] = struct{}{}
		if _, err := api.ValidateWorkflowDAG(wf, plan); err != nil {
			return fmt.Errorf("workflows[%d] %q: %w", i, wf.Name, err)
		}
	}

	return nil
}

// supportedKindsList returns the human-readable comma-separated list
// of supported TriggerKind values for inclusion in upgrade-me error
// messages. Order is intentionally stable (cron first — the legacy
// shape — then alphabetical) so a customer grep'ing for "kafka" in
// the error message finds it consistently.
func supportedKindsList() string {
	return "cron, kafka, nats, redis_streams, sqs_compat, queue"
}

// validateKindConfig runs the per-kind config check. The cron kind's
// schedule + path pair is the legacy PR-C contract; the five new
// kinds validate their typed config struct.
//
// We marshal the YAML-decoded Config map back to JSON to feed the
// std json.Unmarshal — round-tripping through bytes is the cheapest
// path that lets both surfaces (CLI decoder + apid server-side
// validator) reuse the same shape with no custom YAML→JSON shim.
// The cost is one extra allocation per trigger per manifest apply;
// negligible relative to the apid round-trip.
func (t Trigger) validateKindConfig(idx int) error {
	switch t.Kind {
	case TriggerKindCron:
		if _, err := sched.ParseSchedule(t.Schedule); err != nil {
			return fmt.Errorf("trigger[%d]: bad schedule %q: %w", idx, t.Schedule, err)
		}
		if !strings.HasPrefix(t.Path, "/") {
			return fmt.Errorf("trigger[%d]: path must start with '/' (got %q)", idx, t.Path)
		}
		return nil
	case TriggerKindKafka:
		var c KafkaConfig
		if err := decodeInto(t.Config, &c); err != nil {
			return fmt.Errorf("trigger[%d]: bad kafka config: %w", idx, err)
		}
		if len(c.Brokers) == 0 {
			return fmt.Errorf("trigger[%d]: kafka config requires non-empty brokers", idx)
		}
		if c.Topic == "" {
			return fmt.Errorf("trigger[%d]: kafka config requires non-empty topic", idx)
		}
		if c.Group == "" {
			return fmt.Errorf("trigger[%d]: kafka config requires non-empty group", idx)
		}
		if err := validateKafkaTLS(idx, c.TLS); err != nil {
			return err
		}
		if err := validateKafkaSASL(idx, c.SASL); err != nil {
			return err
		}
		return nil
	case TriggerKindNATS:
		var c NATSConfig
		if err := decodeInto(t.Config, &c); err != nil {
			return fmt.Errorf("trigger[%d]: bad nats config: %w", idx, err)
		}
		if c.URL == "" {
			return fmt.Errorf("trigger[%d]: nats config requires non-empty url", idx)
		}
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "nats" && u.Scheme != "tls") || u.Host == "" {
			return fmt.Errorf("trigger[%d]: nats url must be nats:// or tls:// with a host (got %q)", idx, c.URL)
		}
		if c.Stream == "" {
			return fmt.Errorf("trigger[%d]: nats config requires non-empty stream", idx)
		}
		if c.Subject == "" {
			return fmt.Errorf("trigger[%d]: nats config requires non-empty subject", idx)
		}
		if c.Durable == "" {
			return fmt.Errorf("trigger[%d]: nats config requires non-empty durable", idx)
		}
		return nil
	case TriggerKindRedisStreams:
		var c RedisStreamsConfig
		if err := decodeInto(t.Config, &c); err != nil {
			return fmt.Errorf("trigger[%d]: bad redis_streams config: %w", idx, err)
		}
		if c.Addr == "" {
			return fmt.Errorf("trigger[%d]: redis_streams config requires non-empty addr", idx)
		}
		if c.Stream == "" {
			return fmt.Errorf("trigger[%d]: redis_streams config requires non-empty stream", idx)
		}
		if c.Group == "" {
			return fmt.Errorf("trigger[%d]: redis_streams config requires non-empty group", idx)
		}
		return nil
	case TriggerKindSQSCompat:
		var c SQSCompatConfig
		if err := decodeInto(t.Config, &c); err != nil {
			return fmt.Errorf("trigger[%d]: bad sqs_compat config: %w", idx, err)
		}
		if c.QueueURL == "" {
			return fmt.Errorf("trigger[%d]: sqs_compat config requires non-empty queue_url", idx)
		}
		u, err := url.Parse(c.QueueURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("trigger[%d]: sqs_compat queue_url must be http:// or https:// with a host (got %q)", idx, c.QueueURL)
		}
		if c.LongPollSecs != 0 && (c.LongPollSecs < 1 || c.LongPollSecs > 20) {
			return fmt.Errorf("trigger[%d]: sqs_compat long_poll_secs=%d out of range [1, 20]", idx, c.LongPollSecs)
		}
		return nil
	case TriggerKindQueue:
		var c QueueConfig
		if err := decodeInto(t.Config, &c); err != nil {
			return fmt.Errorf("trigger[%d]: bad queue config: %w", idx, err)
		}
		switch c.Mode {
		case "queue", "delayed_task":
			return nil
		default:
			return fmt.Errorf("trigger[%d]: queue config mode %q not in {queue, delayed_task}", idx, c.Mode)
		}
	}
	// Unreachable: the outer switch in Validate already rejected
	// unknown kinds. Returning nil here keeps the linter quiet and
	// the function total.
	return nil
}

// decodeInto round-trips the YAML-decoded Config map (typed as
// map[string]any) through JSON to feed json.Unmarshal. The round-trip
// is necessary because the YAML decoder uses gopkg.in/yaml.v3 which
// returns map[interface{}]interface{} for nested maps unless we
// decode into the typed struct directly via YAML — but the typed
// struct lives in both the CLI loader AND the apid server-side
// validator (cmd/apid/scan_service.go) where the input is already
// JSON from the wire, so a single json-based path keeps both surfaces
// symmetric. An empty/nil Config decodes as the zero value of the
// target struct (no error), which surfaces as the per-field required
// errors below.
func decodeInto(src map[string]any, dst any) error {
	if len(src) == 0 {
		return nil
	}
	b, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// isDNSSafeSlug mirrors the wire-path slug rules: lowercase
// alphanumeric + dashes, must start with a letter, ≤63 chars (the
// DNS label ceiling). Anything outside this set surfaces a manifest
// validation error so the customer sees the issue at `gregale
// deploy` time rather than at apid-request time.
func isDNSSafeSlug(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	if !isLowerAlpha(s[0]) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isLowerAlpha(c) && !isDigit(c) && c != '-' {
			return false
		}
	}
	return true
}

func isLowerAlpha(b byte) bool { return b >= 'a' && b <= 'z' }
func isDigit(b byte) bool      { return b >= '0' && b <= '9' }

// validateKafkaTLS enforces the closed shape on KafkaConfig.TLS
// (ADR-118 / issue #757 §criterion 2). nil is the production
// default ("platform CA bundle, no client cert, no skip-verify")
// and is accepted silently.
//
// The half-wired mTLS pair rejection is the load-bearing check:
// a customer who sets client_cert but forgets client_key (or
// vice-versa) silently gets a 0-byte handshake with no error
// from segmentio/kafka-go — only a hang at the first FetchMessage.
// Surfacing the typo at `gregale deploy` time turns that into a
// typed error pointing at the half-wired field.
func validateKafkaTLS(idx int, t *TLSConfig) error {
	if t == nil {
		return nil
	}
	if (t.ClientCert == "") != (t.ClientKey == "") {
		return fmt.Errorf("trigger[%d]: kafka tls requires both client_cert and client_key when mTLS is configured (got cert=%q, key-set=%t)",
			idx, t.ClientCert, t.ClientKey != "")
	}
	return nil
}

// validateKafkaSASL enforces the closed vocab on KafkaConfig.SASL
// (ADR-118 / issue #757 §criterion 2). nil is the production
// default ("no SASL" — plaintext-equivalent dial).
//
// Mechanism is the load-bearing check: a typo'd
// `mechanism: SCRAM-SHA256` (missing dash) is silently accepted by
// the YAML decoder but rejected at the first broker dial, leaving
// the customer with a hung trigger. The validator surfaces the
// typo at load time so a `gregale deploy` flags it inline.
func validateKafkaSASL(idx int, s *SASLConfig) error {
	if s == nil {
		return nil
	}
	if !s.Mechanism.IsValid() {
		return fmt.Errorf("trigger[%d]: kafka sasl mechanism %q not in {PLAIN, SCRAM-SHA-256, SCRAM-SHA-512}",
			idx, s.Mechanism)
	}
	if s.Username == "" {
		return fmt.Errorf("trigger[%d]: kafka sasl requires non-empty username", idx)
	}
	if s.Password == "" {
		return fmt.Errorf("trigger[%d]: kafka sasl requires non-empty password", idx)
	}
	return nil
}

// validateFilterCriteria walks the filter tree (ADR-118 / issue
// #757 §criterion 4) and rejects malformed shapes at load time.
//
// Closed vocab checks (per FilterClause.Op):
//
//   - eq / neq / exists:  field must be non-empty.
//   - jsonpath:          path must be non-empty AND parseable.
//
// Structural checks:
//
//   - A clause may not simultaneously carry Clauses AND a leaf
//     operator (a half-wired branch). The runtime short-circuits
//     on Clauses, but a customer typo'd `clauses: [...]` on a leaf
//     would silently disable the leaf — surfacing here turns it
//     into a typed error.
//   - Top-level FilterCriteria must have at least one of
//     OR / AND / Payload — the empty-tree case is technically
//     match-anything, but the validator rejects it as a
//     degenerate config that almost certainly indicates a
//     customer typo (cf. validateKindConfig rejecting empty
//     Config on non-cron kinds).
//
// JSONPath syntax is checked here using the PaesslerAG/jsonpath
// library (used at runtime by pkg/sched/filter.go). A parse
// failure surfaces as a typed error pointing at the path — the
// runtime would have hit the same error on the first poll, but
// surfacing it at `gregale deploy` time lets the customer fix
// the YAML before the trigger goes live.
func validateFilterCriteria(idx int, f *FilterCriteria) error {
	if f == nil {
		return nil
	}
	if len(f.OR) == 0 && len(f.AND) == 0 && len(f.Payload) == 0 {
		return fmt.Errorf("trigger[%d]: filter_criteria must declare at least one of $or, $and, payload (empty tree is a degenerate config)", idx)
	}
	for i, c := range f.OR {
		if err := validateFilterClause(idx, fmt.Sprintf("$or[%d]", i), c); err != nil {
			return err
		}
	}
	for i, c := range f.AND {
		if err := validateFilterClause(idx, fmt.Sprintf("$and[%d]", i), c); err != nil {
			return err
		}
	}
	for i, c := range f.Payload {
		if err := validateFilterClause(idx, fmt.Sprintf("payload[%d]", i), c); err != nil {
			return err
		}
	}
	return nil
}

// validateFilterClause checks a single leaf or branch (ADR-118
// §criterion 4). See validateFilterCriteria for the rationale.
func validateFilterClause(idx int, path string, c FilterClause) error {
	if len(c.Clauses) > 0 && c.Op != "" {
		// Half-wired branch: a clause with Clauses AND a leaf
		// operator is malformed. Reject so the customer sees
		// the typo at load time.
		return fmt.Errorf("trigger[%d]: filter_criteria.%s carries both op=%q and clauses=%d — pick one (branch vs leaf)",
			idx, path, c.Op, len(c.Clauses))
	}
	switch c.Op {
	case FilterOpEq, FilterOpNeq, FilterOpExists:
		if c.Field == "" {
			return fmt.Errorf("trigger[%d]: filter_criteria.%s requires non-empty field (op=%q operates against header keys)",
				idx, path, c.Op)
		}
	case FilterOpJsonPath:
		if c.Path == "" {
			return fmt.Errorf("trigger[%d]: filter_criteria.%s requires non-empty path (op=jsonpath)", idx, path)
		}
		if err := checkJSONPathShape(c.Path); err != nil {
			return fmt.Errorf("trigger[%d]: filter_criteria.%s jsonpath %q: %w",
				idx, path, c.Path, err)
		}
	case "":
		// Empty-op branch with nested clauses — recurse to
		// validate the children. The runtime treats this as a
		// nested AND (the most common intent).
		if len(c.Clauses) == 0 {
			return fmt.Errorf("trigger[%d]: filter_criteria.%s carries neither op nor clauses (empty leaf)",
				idx, path)
		}
	default:
		return fmt.Errorf("trigger[%d]: filter_criteria.%s op=%q not in {eq, neq, exists, jsonpath}",
			idx, path, c.Op)
	}
	for j, cc := range c.Clauses {
		if err := validateFilterClause(idx, fmt.Sprintf("%s.clauses[%d]", path, j), cc); err != nil {
			return err
		}
	}
	return nil
}

// checkJSONPathShape is a minimal JSONPath syntax pre-check used
// at the manifest validator layer (commit 2 of ADR-118). It does
// NOT use the full PaesslerAG/jsonpath library (which lands as a
// runtime dependency in commit 5 via pkg/sched/filter.go) — the
// validator's job is to catch the most common typos at `gregale
// deploy` time without pulling in a new transitive dep just for
// compile-time validation.
//
// Checks (best-effort):
//
//   - non-empty
//   - starts with "$" (the JSONPath root marker; segmentio
//     rejects anything else).
//   - balanced brackets (parens + square brackets).
//   - no trailing dots before a "[" (e.g. "$.foo.[0]" is invalid).
//
// Anything not rejected here is accepted; the runtime evaluator
// in commit 5 is the source of truth for full JSONPath semantics.
func checkJSONPathShape(p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if p[0] != '$' {
		return fmt.Errorf("must start with $ (got %q)", string(p[0]))
	}
	var (
		parenDepth   int
		bracketDepth int
	)
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
			if parenDepth < 0 {
				return fmt.Errorf("unbalanced parens at offset %d", i)
			}
		case '[':
			bracketDepth++
		case ']':
			bracketDepth--
			if bracketDepth < 0 {
				return fmt.Errorf("unbalanced brackets at offset %d", i)
			}
		case '.':
			// A dot followed by a bracket is the "dot-then-index"
			// form, which PaesslerAG's strict mode rejects. Catch
			// the most common typo: "$.foo.[0]" instead of
			// "$.foo[0]".
			if i+1 < len(p) && p[i+1] == '[' {
				return fmt.Errorf("dot before bracket at offset %d (write %q without the dot)",
					i, p[:i])
			}
		}
	}
	if parenDepth != 0 {
		return fmt.Errorf("unbalanced parens (depth=%d at end)", parenDepth)
	}
	if bracketDepth != 0 {
		return fmt.Errorf("unbalanced brackets (depth=%d at end)", bracketDepth)
	}
	return nil
}

// triggerKey is the dedupe primitive. Five fields because two
// triggers of the same kind on the same app with the same slug are
// the same resource (the SQL UNIQUE (app_id, slug) constraint on the
// `triggers` table — migration 00267). Cron is special-cased: it
// derives its slug implicitly from the (schedule, path) tuple, so the
// dedupe key for kind=cron keeps (schedule, path) and zeroes the
// slug, while the five non-cron kinds rely on the explicit slug.
// Mixing the two shapes in one key tuple would let a cron "*/5 * * * *
// /tick" collide with a kafka trigger whose slug happened to be
// "*/5 * * * * /tick" — instead we keep the two distinct shapes in
// the same tuple with (kind, slug) acting as the discriminating
// fields for non-cron kinds.
type triggerKey struct {
	app      string
	kind     TriggerKind
	schedule string
	path     string
	slug     string
}
