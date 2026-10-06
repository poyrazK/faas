package flags

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPropagationContextRoundTripAndCanonicalOrder(t *testing.T) {
	appID, environmentID, customerID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	context := PropagationContext{
		Version:    PropagationContextVersion,
		CustomerID: customerID,
		Decisions: []PropagationDecision{
			{Decision: Decision{Flag: "z-export", Value: true, ConfigVersion: 4, RuleID: "customers", Reason: "rule_match", Source: "configuration"}, Origin: EvidenceOrigin{AppID: appID, EnvironmentID: environmentID}},
			{Decision: Decision{Flag: "a-checkout", Value: "control", Type: "variant", ConfigVersion: 9, Reason: "default", Source: "configuration"}, Origin: EvidenceOrigin{AppID: appID, EnvironmentID: environmentID}},
		},
	}
	encoded, err := EncodePropagationHeader(context)
	if err != nil {
		t.Fatalf("encode propagation context: %v", err)
	}
	decoded, err := DecodePropagationHeader(encoded)
	if err != nil {
		t.Fatalf("decode propagation context: %v", err)
	}
	if decoded.CustomerID != customerID || len(decoded.Decisions) != 2 || decoded.Decisions[0].Flag != "a-checkout" || decoded.Decisions[1].Flag != "z-export" {
		t.Fatalf("decoded context = %+v", decoded)
	}
}

func TestPropagationContextRejectsAmbiguousOrInvalidData(t *testing.T) {
	appID, environmentID, customerID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	valid := PropagationContext{
		Version: PropagationContextVersion, CustomerID: customerID,
		Decisions: []PropagationDecision{{
			Decision: Decision{Flag: "export", Value: true, ConfigVersion: 1, RuleID: "selected", Reason: "rule_match", Source: "configuration"},
			Origin:   EvidenceOrigin{AppID: appID, EnvironmentID: environmentID},
		}},
	}
	encoded, err := EncodePropagationHeader(valid)
	if err != nil {
		t.Fatal(err)
	}
	for name, header := range map[string]string{
		"invalid base64": encoded + "!",
		"oversized":      strings.Repeat("a", MaxPropagationContextHeaderLen+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePropagationHeader(header); err == nil {
				t.Fatal("invalid context was accepted")
			}
		})
	}

	mutations := map[string]func(*PropagationContext){
		"no selected decisions": func(c *PropagationContext) { c.Decisions = nil },
		"inconsistent fallback": func(c *PropagationContext) { c.Decisions[0].Source = "fallback" },
		"missing origin":        func(c *PropagationContext) { c.Decisions[0].Origin.AppID = "" },
		"duplicate flag":        func(c *PropagationContext) { c.Decisions = append(c.Decisions, c.Decisions[0]) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			bad := valid
			bad.Decisions = append([]PropagationDecision(nil), valid.Decisions...)
			mutate(&bad)
			if _, err := EncodePropagationHeader(bad); err == nil {
				t.Fatal("invalid context was encoded")
			}
		})
	}
}

func TestCanonicalEvidenceAcceptsInheritedDecisionWithOrigin(t *testing.T) {
	appID, environmentID := uuid.NewString(), uuid.NewString()
	raw, err := json.Marshal([]Evidence{{
		Decision: Decision{
			Flag: "export", Value: true, ConfigVersion: 3, RuleID: "selected", Reason: "rule_match", Source: "inherited",
			InheritedFrom: &EvidenceOrigin{AppID: appID, EnvironmentID: environmentID},
		},
		Used: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalEvidence(raw); err != nil {
		t.Fatalf("canonicalize inherited evidence: %v", err)
	}
	withoutOrigin := strings.Replace(string(raw), `,"inherited_from":{"app_id":"`+appID+`","environment_id":"`+environmentID+`"}`, "", 1)
	if _, err := CanonicalEvidence([]byte(withoutOrigin)); err == nil {
		t.Fatal("inherited evidence without origin was accepted")
	}
}

func TestPropagationContextRejectsNonCanonicalBase64(t *testing.T) {
	context := PropagationContext{Version: PropagationContextVersion, CustomerID: uuid.NewString(), Decisions: []PropagationDecision{{
		Decision: Decision{Flag: "export", Value: true, ConfigVersion: 1, Reason: "default", Source: "configuration"},
		Origin:   EvidenceOrigin{AppID: uuid.NewString(), EnvironmentID: uuid.NewString()},
	}}}
	encoded, err := EncodePropagationHeader(context)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePropagationHeader(base64.URLEncoding.EncodeToString(decoded)); err == nil {
		t.Fatal("padded base64url was accepted")
	}
}
