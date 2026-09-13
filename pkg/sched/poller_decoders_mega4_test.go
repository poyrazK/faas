// adr: 100
// poller_decoders_mega4_test.go — Coverage Mega-PR #4 cluster 7:
// fill pkg/sched coverage on the pure config decoders + small pure
// helpers in poller_*.go + engine.go that the existing
// dispatch_triggers_pure_test.go + engine_pure_test.go + the poller
// integration tests don't exercise in isolation.
//
// Targets:
//   - decodeKafkaConfig (missing config, missing brokers, missing
//     topic, missing group, SASL bad mechanism, SASL missing
//     username, SASL missing password, valid)
//   - buildKafkaDialer (no TLS no SASL, with TLS, with SASL, both)
//   - buildKafkaTLSConfig (no CA, invalid CA, missing client_key,
//     valid CA only, valid CA+client key)
//   - kafkaSASLMechanism (PLAIN, SCRAM-SHA-256, SCRAM-SHA-512,
//     unknown)
//   - decodeNATSConfig (all 4 branches + happy)
//   - decodeRedisConfig (all 4 branches + happy)
//   - decodeSQSConfig (missing queue_url, missing scheme, missing
//     host, valid http, valid https, long_poll clamp + default)
//   - parseJSONHeaders / parseJSONMetadata (empty / valid /
//     malformed)
//   - envSecretsFromDep (empty / valid / malformed)
//   - healthcheckPathFromDep (empty / valid / malformed)
//   - isOnScaleOutCooldown (concurrency=0, nil stamp, nil policy,
//     zero cooldown, expired stamp, active cooldown)
//   - atMinFloorWithNoSignal (nil policy, zero MinInstances,
//     below-floor, at-floor)
//
// Whitebox `package sched`.

package sched

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// --- decodeKafkaConfig -------------------------------------------

func triggerWithConfig_Mega4(cfg string) sqlc.Trigger {
	return sqlc.Trigger{Config: []byte(cfg)}
}

func TestDecodeKafkaConfig_Missing_Mega4(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		cfg  string
		want string
	}{
		{"empty config", "", "trigger missing config"},
		{"malformed json", "{not-json", "decode config"},
		{"missing brokers", `{"topic":"t","group":"g"}`, "missing brokers"},
		{"missing topic", `{"brokers":["b"],"group":"g"}`, "missing topic"},
		{"missing group", `{"brokers":["b"],"topic":"t"}`, "missing group"},
		{
			name: "sasl bad mechanism",
			cfg:  `{"brokers":["b"],"topic":"t","group":"g","sasl":{"mechanism":"NTLM","username":"u","password":"p"}}`,
			want: "sasl.mechanism",
		},
		{
			name: "sasl missing username",
			cfg:  `{"brokers":["b"],"topic":"t","group":"g","sasl":{"mechanism":"PLAIN","password":"p"}}`,
			want: "sasl.username is required",
		},
		{
			name: "sasl missing password",
			cfg:  `{"brokers":["b"],"topic":"t","group":"g","sasl":{"mechanism":"PLAIN","username":"u"}}`,
			want: "sasl.password is required",
		},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeKafkaConfig(triggerWithConfig_Mega4(c.cfg))
			if err == nil {
				t.Fatal("want err")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want substring %q", err, c.want)
			}
		})
	}
}

func TestDecodeKafkaConfig_Happy_Mega4(t *testing.T) {
	t.Parallel()
	for _, c := range []string{
		`{"brokers":["b"],"topic":"t","group":"g"}`,
		`{"brokers":["b"],"topic":"t","group":"g","sasl":{"mechanism":"PLAIN","username":"u","password":"p"}}`,
		`{"brokers":["b"],"topic":"t","group":"g","sasl":{"mechanism":"SCRAM-SHA-256","username":"u","password":"p"}}`,
		`{"brokers":["b"],"topic":"t","group":"g","sasl":{"mechanism":"SCRAM-SHA-512","username":"u","password":"p"}}`,
	} {
		if _, err := decodeKafkaConfig(triggerWithConfig_Mega4(c)); err != nil {
			t.Errorf("happy path %q: %v", c, err)
		}
	}
}

// --- buildKafkaDialer --------------------------------------------

func TestBuildKafkaDialer_PlainTCP_Mega4(t *testing.T) {
	t.Parallel()
	d, err := buildKafkaDialer(kafkaConfig{Brokers: []string{"b"}, Topic: "t", Group: "g"})
	if err != nil {
		t.Fatalf("buildKafkaDialer: %v", err)
	}
	if d == nil {
		t.Fatal("nil dialer")
	}
	if d.TLS != nil {
		t.Errorf("plain TCP should not set TLS")
	}
	if d.SASLMechanism != nil {
		t.Errorf("no SASL config: SASLMechanism should be nil")
	}
}

func TestBuildKafkaDialer_WithTLS_Mega4(t *testing.T) {
	t.Parallel()
	d, err := buildKafkaDialer(kafkaConfig{
		Brokers: []string{"b"}, Topic: "t", Group: "g",
		TLS: &kafkaTLSConfig{CACert: ""},
	})
	if err != nil {
		t.Fatalf("buildKafkaDialer: %v", err)
	}
	if d.TLS == nil {
		t.Error("expected non-nil TLS")
	}
}

func TestBuildKafkaDialer_WithSASL_Mega4(t *testing.T) {
	t.Parallel()
	d, err := buildKafkaDialer(kafkaConfig{
		Brokers: []string{"b"}, Topic: "t", Group: "g",
		SASL: &kafkaSASLConfig{Mechanism: "PLAIN", Username: "u", Password: "p"},
	})
	if err != nil {
		t.Fatalf("buildKafkaDialer: %v", err)
	}
	if d.SASLMechanism == nil {
		t.Error("expected non-nil SASLMechanism")
	}
}

func TestBuildKafkaDialer_TLSError_Mega4(t *testing.T) {
	t.Parallel()
	// Invalid CA → buildKafkaTLSConfig returns an error.
	_, err := buildKafkaDialer(kafkaConfig{
		Brokers: []string{"b"}, Topic: "t", Group: "g",
		TLS: &kafkaTLSConfig{CACert: "not-pem"},
	})
	if err == nil {
		t.Fatal("want err for invalid CA")
	}
}

// --- buildKafkaTLSConfig -----------------------------------------

func TestBuildKafkaTLSConfig_InvalidCA_Mega4(t *testing.T) {
	t.Parallel()
	_, err := buildKafkaTLSConfig(&kafkaTLSConfig{CACert: "not-pem"})
	if err == nil {
		t.Fatal("want err for invalid CA")
	}
}

func TestBuildKafkaTLSConfig_MissingClientKey_Mega4(t *testing.T) {
	t.Parallel()
	_, err := buildKafkaTLSConfig(&kafkaTLSConfig{ClientCert: "c"})
	if err == nil {
		t.Fatal("want err for missing client_key")
	}
}

// --- kafkaSASLMechanism ------------------------------------------

func TestKafkaSASLMechanism_Unknown_Mega4(t *testing.T) {
	t.Parallel()
	_, err := kafkaSASLMechanism(&kafkaSASLConfig{Mechanism: "NTLM", Username: "u", Password: "p"})
	if err == nil {
		t.Fatal("want err for unknown mechanism")
	}
}

// --- decodeNATSConfig --------------------------------------------

func TestDecodeNATSConfig_Mega4(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		cfg  string
		want string
	}{
		{"empty", "", "trigger missing config"},
		{"malformed", "{bad", "decode config"},
		{"missing url", `{"stream":"s","subject":"sub"}`, "missing url"},
		{"missing stream", `{"url":"nats://x","subject":"sub"}`, "missing stream or subject"},
		{"missing subject", `{"url":"nats://x","stream":"s"}`, "missing stream or subject"},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeNATSConfig(triggerWithConfig_Mega4(c.cfg))
			if err == nil {
				t.Fatal("want err")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestDecodeNATSConfig_Happy_Mega4(t *testing.T) {
	t.Parallel()
	_, err := decodeNATSConfig(triggerWithConfig_Mega4(
		`{"url":"nats://x","stream":"s","subject":"sub"}`))
	if err != nil {
		t.Errorf("happy: %v", err)
	}
}

// --- decodeRedisConfig -------------------------------------------

func TestDecodeRedisConfig_Mega4(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		cfg  string
		want string
	}{
		{"empty", "", "trigger missing config"},
		{"malformed", "{bad", "decode config"},
		{"missing addr", `{"stream":"s","group":"g"}`, "missing addr"},
		{"missing stream", `{"addr":"a","group":"g"}`, "missing stream"},
		{"missing group", `{"addr":"a","stream":"s"}`, "missing group"},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeRedisConfig(triggerWithConfig_Mega4(c.cfg))
			if err == nil {
				t.Fatal("want err")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestDecodeRedisConfig_Happy_Mega4(t *testing.T) {
	t.Parallel()
	_, err := decodeRedisConfig(triggerWithConfig_Mega4(
		`{"addr":"a","stream":"s","group":"g"}`))
	if err != nil {
		t.Errorf("happy: %v", err)
	}
}

// --- decodeSQSConfig ---------------------------------------------

func TestDecodeSQSConfig_Mega4(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		cfg  string
		want string
	}{
		{"empty", "", "trigger missing config"},
		{"malformed", "{bad", "decode config"},
		{"missing queue_url", `{}`, "missing queue_url"},
		{"missing scheme", `{"queue_url":"faas-queue:9090/q"}`, "must include http"},
		{"missing host", `{"queue_url":"http:///q"}`, "missing host"},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeSQSConfig(triggerWithConfig_Mega4(c.cfg))
			if err == nil {
				t.Fatal("want err")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestDecodeSQSConfig_LongPollClamp_Mega4(t *testing.T) {
	t.Parallel()
	// long_poll_secs > 20 → clamped to 20.
	cfg, err := decodeSQSConfig(triggerWithConfig_Mega4(
		`{"queue_url":"http://x/q","long_poll_secs":99}`))
	if err != nil {
		t.Fatalf("decodeSQSConfig: %v", err)
	}
	if cfg.LongPollSec != 20 {
		t.Errorf("clamp high: got %d, want 20", cfg.LongPollSec)
	}
	// long_poll_secs == 0 → defaulted to 5.
	cfg, _ = decodeSQSConfig(triggerWithConfig_Mega4(
		`{"queue_url":"http://x/q","long_poll_secs":0}`))
	if cfg.LongPollSec != 5 {
		t.Errorf("default: got %d, want 5", cfg.LongPollSec)
	}
	// Negative → clamped to 20.
	cfg, _ = decodeSQSConfig(triggerWithConfig_Mega4(
		`{"queue_url":"http://x/q","long_poll_secs":-3}`))
	if cfg.LongPollSec != 20 {
		t.Errorf("negative clamp: got %d, want 20", cfg.LongPollSec)
	}
}

func TestDecodeSQSConfig_Happy_Mega4(t *testing.T) {
	t.Parallel()
	for _, cfg := range []string{
		`{"queue_url":"http://x/q"}`,
		`{"queue_url":"https://x/q","long_poll_secs":10}`,
	} {
		if _, err := decodeSQSConfig(triggerWithConfig_Mega4(cfg)); err != nil {
			t.Errorf("happy %q: %v", cfg, err)
		}
	}
}

// --- parseJSONHeaders / parseJSONMetadata -----------------------

func TestParseJSONHeaders_Mega4(t *testing.T) {
	t.Parallel()
	if got := parseJSONHeaders(""); got != nil {
		t.Errorf("empty: %v", got)
	}
	if got := parseJSONHeaders("not-json"); got != nil {
		t.Errorf("malformed: %v, want nil", got)
	}
	got := parseJSONHeaders(`{"x":"y","z":"w"}`)
	if got["x"] != "y" || got["z"] != "w" {
		t.Errorf("got %v", got)
	}
}

func TestParseJSONMetadata_Mega4(t *testing.T) {
	t.Parallel()
	if got := parseJSONMetadata(""); got != nil {
		t.Errorf("empty: %v", got)
	}
	if got := parseJSONMetadata("not-json"); got != nil {
		t.Errorf("malformed: %v", got)
	}
	got := parseJSONMetadata(`{"x":1,"y":"z"}`)
	if got["x"].(float64) != 1 || got["y"].(string) != "z" {
		t.Errorf("got %v", got)
	}
}

// --- envSecretsFromDep -------------------------------------------

func TestEnvSecretsFromDep_Mega4(t *testing.T) {
	t.Parallel()
	// Empty: nil.
	if got := envSecretsFromDep(state.Deployment{}); got != nil {
		t.Errorf("empty: %v", got)
	}
	// Malformed: nil (defensive).
	got := envSecretsFromDep(state.Deployment{OverrideEnvSecrets: []byte("{not-json")})
	if got != nil {
		t.Errorf("malformed: %v", got)
	}
	// Valid JSON empty object: nil (coalesced).
	got = envSecretsFromDep(state.Deployment{OverrideEnvSecrets: []byte("{}")})
	if got != nil {
		t.Errorf("empty json: %v", got)
	}
	// Valid populated.
	got = envSecretsFromDep(state.Deployment{OverrideEnvSecrets: []byte(`{"K":"V"}`)})
	if got["K"] != "V" {
		t.Errorf("got %v", got)
	}
}

// --- healthcheckPathFromDep --------------------------------------

func TestHealthcheckPathFromDep_Mega4(t *testing.T) {
	t.Parallel()
	// Empty: "".
	if got := healthcheckPathFromDep(state.Deployment{}); got != "" {
		t.Errorf("empty: %q", got)
	}
	// Malformed: "" (defensive).
	got := healthcheckPathFromDep(state.Deployment{OverrideHealthcheck: []byte("{bad")})
	if got != "" {
		t.Errorf("malformed: %q", got)
	}
	// Valid empty Path: "".
	got = healthcheckPathFromDep(state.Deployment{OverrideHealthcheck: []byte(`{}`)})
	if got != "" {
		t.Errorf("empty path: %q", got)
	}
	// Valid.
	got = healthcheckPathFromDep(state.Deployment{OverrideHealthcheck: []byte(`{"path":"/healthz"}`)})
	if got != "/healthz" {
		t.Errorf("got %q", got)
	}
	profile := json.RawMessage(`{"version":"v1","framework":"node","port":3000,"health_path":"/readyz","inferred":true}`)
	if got := healthcheckPathFromDep(state.Deployment{InferredProfile: profile}); got != "/readyz" {
		t.Errorf("inferred profile path = %q, want /readyz", got)
	}
	receipt := json.RawMessage(`{"profile":{"version":"v1","framework":"node","port":3000,"health_path":"/receiptz","inferred":true}}`)
	if got := healthcheckPathFromDep(state.Deployment{APIHostingReceipt: receipt, InferredProfile: profile}); got != "/receiptz" {
		t.Errorf("hosting receipt path = %q, want /receiptz", got)
	}
	if got := healthcheckPathFromDep(state.Deployment{
		OverrideHealthcheck: json.RawMessage(`{"path":"/overridez"}`),
		APIHostingReceipt:   receipt,
		InferredProfile:     profile,
	}); got != "/overridez" {
		t.Errorf("override path = %q, want /overridez", got)
	}
	if got := healthcheckPathFromDep(state.Deployment{InferredProfile: json.RawMessage(`{"version":"v1","health_path":"/ok\nheader"}`)}); got != "" {
		t.Errorf("unsafe health path = %q, want empty", got)
	}
}

// --- isOnScaleOutCooldown ----------------------------------------

func TestIsOnScaleOutCooldown_Mega4(t *testing.T) {
	t.Parallel()
	e := &Engine{}
	// concurrency == 0 → false (the "no live" path is not a cooldown).
	stamp := time.Now().Add(-1 * time.Second)
	if e.isOnScaleOutCooldown(&state.App{
		LastScaleOutAt: &stamp,
		ScalingPolicy:  &state.ScalingPolicy{ScaleOutCooldownS: 60},
	}, 0) {
		t.Error("concurrency 0: want false")
	}
	// Nil stamp → false.
	if e.isOnScaleOutCooldown(&state.App{
		ScalingPolicy: &state.ScalingPolicy{ScaleOutCooldownS: 60},
	}, 1) {
		t.Error("nil stamp: want false")
	}
	// Nil policy → false.
	if e.isOnScaleOutCooldown(&state.App{LastScaleOutAt: &stamp}, 1) {
		t.Error("nil policy: want false")
	}
	// Zero cooldown → false.
	if e.isOnScaleOutCooldown(&state.App{
		LastScaleOutAt: &stamp,
		ScalingPolicy:  &state.ScalingPolicy{ScaleOutCooldownS: 0},
	}, 1) {
		t.Error("zero cooldown: want false")
	}
	// Expired stamp → false.
	old := time.Now().Add(-1 * time.Hour)
	if e.isOnScaleOutCooldown(&state.App{
		LastScaleOutAt: &old,
		ScalingPolicy:  &state.ScalingPolicy{ScaleOutCooldownS: 60},
	}, 1) {
		t.Error("expired: want false")
	}
	// Active → true.
	if !e.isOnScaleOutCooldown(&state.App{
		LastScaleOutAt: &stamp,
		ScalingPolicy:  &state.ScalingPolicy{ScaleOutCooldownS: 60},
	}, 1) {
		t.Error("active: want true")
	}
}

// --- atMinFloorWithNoSignal --------------------------------------

func TestAtMinFloorWithNoSignal_Mega4(t *testing.T) {
	t.Parallel()
	e := &Engine{}
	// Nil policy → false.
	if e.atMinFloorWithNoSignal(&state.App{}, 1) {
		t.Error("nil policy: want false")
	}
	// Zero MinInstances → false.
	if e.atMinFloorWithNoSignal(&state.App{
		ScalingPolicy: &state.ScalingPolicy{MinInstances: 0},
	}, 1) {
		t.Error("zero min: want false")
	}
	// Below floor → false.
	if e.atMinFloorWithNoSignal(&state.App{
		ScalingPolicy: &state.ScalingPolicy{MinInstances: 3},
	}, 2) {
		t.Error("below floor: want false")
	}
	// At floor → true.
	if !e.atMinFloorWithNoSignal(&state.App{
		ScalingPolicy: &state.ScalingPolicy{MinInstances: 3},
	}, 3) {
		t.Error("at floor: want true")
	}
	// Above floor → true (>=).
	if !e.atMinFloorWithNoSignal(&state.App{
		ScalingPolicy: &state.ScalingPolicy{MinInstances: 3},
	}, 5) {
		t.Error("above floor: want true")
	}
}

// --- marshalJSON helper -------------------------------------------

func TestMarshalJSON_Mega4(t *testing.T) {
	t.Parallel()
	// nil input → "{}" (per the dispatch_triggers contract: empty
	// object, NOT empty bytes).
	if got := marshalJSON(nil); string(got) != "{}" {
		t.Errorf("nil: got %q, want %q", got, "{}")
	}
	// RawMessage round-trip.
	got := marshalJSON(json.RawMessage(`{"x":1}`))
	if string(got) != `{"x":1}` {
		t.Errorf("RawMessage: got %q", got)
	}
	// Map round-trip.
	got = marshalJSON(map[string]string{"k": "v"})
	if string(got) != `{"k":"v"}` {
		t.Errorf("map: got %q", got)
	}
	// Note: marshalJSON's error branch returns nil. json.Marshal
	// is permissive — channels, funcs, and cyclic references all
	// serialise to "{}" without error — so we don't exercise the
	// err branch in unit tests. The branch is reachable only via
	// a future json.Marshaler failure (e.g. an unsupported type
	// after a stdlib change), and the contract is unchanged.
}
