// adr: 117
//
// ADR-117 defines the deployment stage/receipt contract that the
// post-readiness hosting smoke closes out. These tests pin the observability
// of its gateway-side bypass, not the bypass logic itself.
//
// They exist because production sat in a state nobody could diagnose: every
// cd-platform run failed with `health probe reached deployment ""`, and the
// gateway emitted nothing on either the receive or the validate path — a
// challenge that never arrived and a challenge that arrived but did not match
// were indistinguishable from outside the process.

package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func smokeBackend(t *testing.T) (*PGBackend, *prometheus.Registry) {
	t.Helper()
	// NewMetrics owns its registry; read it back rather than injecting one.
	m := NewMetrics()
	return &PGBackend{
		smokeChallenges: map[string][]deploymentSmokeChallenge{},
		metrics:         m,
	}, m.Registry()
}

// counterValue reads one labelled counter, returning 0 when the series has
// not been observed. A missing series and a zero series mean the same thing
// to an operator reading the dashboard.
func counterValue(t *testing.T, reg *prometheus.Registry, name, outcome string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "outcome" && label.GetValue() == outcome {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

func TestSmokeValidationOutcomesAreDistinguishable(t *testing.T) {
	const (
		appID = "app-1"
		depID = "dep-1"
		token = "tok-1"
	)

	t.Run("no challenge stored", func(t *testing.T) {
		b, reg := smokeBackend(t)
		if b.ValidateDeploymentSmoke(appID, depID, token) {
			t.Fatal("validated with no challenge stored")
		}
		if got := counterValue(t, reg, "gateway_smoke_validation_total", "no_challenge"); got != 1 {
			t.Fatalf("no_challenge = %v, want 1", got)
		}
	})

	t.Run("challenge stored and token matches", func(t *testing.T) {
		b, reg := smokeBackend(t)
		b.AuthorizeDeploymentSmoke(appID, depID, token, time.Now().Add(time.Minute))
		if got := counterValue(t, reg, "gateway_smoke_challenge_total", "stored"); got != 1 {
			t.Fatalf("challenge stored = %v, want 1", got)
		}
		if !b.ValidateDeploymentSmoke(appID, depID, token) {
			t.Fatal("a stored, unexpired, matching challenge did not validate")
		}
		if got := counterValue(t, reg, "gateway_smoke_validation_total", "match"); got != 1 {
			t.Fatalf("match = %v, want 1", got)
		}
	})

	// The distinction that matters most in the field: a challenge IS present
	// but the presented token is wrong, versus none present at all. These
	// point at completely different causes and were previously identical.
	t.Run("challenge stored but token differs", func(t *testing.T) {
		b, reg := smokeBackend(t)
		b.AuthorizeDeploymentSmoke(appID, depID, token, time.Now().Add(time.Minute))
		if b.ValidateDeploymentSmoke(appID, depID, "other-token") {
			t.Fatal("validated a token that was never issued")
		}
		if got := counterValue(t, reg, "gateway_smoke_validation_total", "token_mismatch"); got != 1 {
			t.Fatalf("token_mismatch = %v, want 1", got)
		}
		if got := counterValue(t, reg, "gateway_smoke_validation_total", "no_challenge"); got != 0 {
			t.Fatalf("a present-but-wrong token was reported as no_challenge (%v)", got)
		}
	})

	t.Run("challenge expired", func(t *testing.T) {
		b, _ := smokeBackend(t)
		// Store directly: AuthorizeDeploymentSmoke refuses an already-expired
		// challenge, and the case under test is one that expires while held.
		key := smokeChallengeKey(appID, depID)
		b.smokeChallenges[key] = []deploymentSmokeChallenge{
			{token: token, expiresAt: time.Now().Add(-time.Second)},
		}
		m := NewMetrics()
		b.metrics = m
		reg := m.Registry()
		if b.ValidateDeploymentSmoke(appID, depID, token) {
			t.Fatal("validated an expired challenge")
		}
		if got := counterValue(t, reg, "gateway_smoke_validation_total", "expired"); got != 1 {
			t.Fatalf("expired = %v, want 1", got)
		}
		if _, ok := b.smokeChallenges[key]; ok {
			t.Fatal("a fully expired key was left in the map")
		}
	})

	t.Run("empty token", func(t *testing.T) {
		b, reg := smokeBackend(t)
		if b.ValidateDeploymentSmoke(appID, depID, "") {
			t.Fatal("validated an empty token")
		}
		if got := counterValue(t, reg, "gateway_smoke_validation_total", "missing_token"); got != 1 {
			t.Fatalf("missing_token = %v, want 1", got)
		}
	})
}

// TestSmokeChallengeRejectedIsCounted pins the receive side. An already-expired
// or incomplete challenge is dropped on arrival; without this counter that
// drop is invisible and looks identical downstream to a notification that was
// never published.
func TestSmokeChallengeRejectedIsCounted(t *testing.T) {
	b, reg := smokeBackend(t)
	b.AuthorizeDeploymentSmoke("app-1", "dep-1", "tok", time.Now().Add(-time.Minute))
	if got := counterValue(t, reg, "gateway_smoke_challenge_total", "rejected"); got != 1 {
		t.Fatalf("rejected = %v, want 1", got)
	}
	if got := counterValue(t, reg, "gateway_smoke_challenge_total", "stored"); got != 0 {
		t.Fatalf("an expired challenge was counted as stored (%v)", got)
	}
}

// TestSmokeMetricsAreNilSafe keeps the observability from becoming a new
// failure mode on a backend built without a registry.
func TestSmokeMetricsAreNilSafe(t *testing.T) {
	b := &PGBackend{smokeChallenges: map[string][]deploymentSmokeChallenge{}}
	b.AuthorizeDeploymentSmoke("app-1", "dep-1", "tok", time.Now().Add(time.Minute))
	if !b.ValidateDeploymentSmoke("app-1", "dep-1", "tok") {
		t.Fatal("nil metrics changed validation behaviour")
	}
	var nilBackend *PGBackend
	if nilBackend.ValidateDeploymentSmoke("a", "b", "c") {
		t.Fatal("nil backend validated")
	}
}

// TestSmokeMetricNamesAndHelp pins the series names the runbook and any future
// alert reference. Renaming them silently would break both.
func TestSmokeMetricNamesAndHelp(t *testing.T) {
	m := NewMetrics()
	reg := m.Registry()
	m.ObserveSmokeChallenge("stored")
	m.ObserveSmokeValidation("match")

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	want := map[string]bool{
		"gateway_smoke_challenge_total":  false,
		"gateway_smoke_validation_total": false,
	}
	for _, family := range families {
		if _, ok := want[family.GetName()]; !ok {
			continue
		}
		want[family.GetName()] = true
		if family.GetType() != dto.MetricType_COUNTER {
			t.Errorf("%s is %v, want a counter", family.GetName(), family.GetType())
		}
		if !strings.Contains(family.GetHelp(), "smoke") {
			t.Errorf("%s help does not describe the smoke path", family.GetName())
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("%s was not registered", name)
		}
	}
}
