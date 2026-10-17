// adr: 570
package api

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"
)

// A sealed traffic-policy copy encodes a condition's identity, not its
// compiled form. The decoded program must never evaluate on its own: the seal
// rebinds the original program or fails closed.
func TestEdgeRuleMatchProgramJSONCarriesIdentityOnly(t *testing.T) {
	blocked, err := CompileEdgeRuleList(EdgeRuleListKindIP, []string{"203.0.113.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	var expr EdgeRuleMatchExpr
	if err := json.Unmarshal([]byte(`{"field":"client_ip","op":"in_list","list":"blocked"}`), &expr); err != nil {
		t.Fatal(err)
	}
	original, err := CompileEdgeRuleMatchWithLists(&expr, EdgeRuleLists{"blocked": blocked})
	if err != nil {
		t.Fatal(err)
	}
	in := EdgeRuleMatchInput{Method: "GET", ClientIP: net.ParseIP("203.0.113.9"), Headers: http.Header{}}
	if !original.Bound() || !original.Matches(in) {
		t.Fatal("compiled condition does not match its list")
	}
	type holder struct{ Match, Never, Unset *EdgeRuleMatchProgram }
	encoded, err := json.Marshal(holder{Match: original, Never: NeverMatchingEdgeRuleProgram()})
	if err != nil {
		t.Fatal(err)
	}
	var copied holder
	if err := json.Unmarshal(encoded, &copied); err != nil {
		t.Fatalf("decode %s: %v", encoded, err)
	}
	if copied.Unset != nil || !copied.Unset.Bound() {
		t.Fatal("a nil (always-matching) condition must stay nil and bound")
	}
	for _, program := range []*EdgeRuleMatchProgram{copied.Match, copied.Never} {
		if program == nil || program.Bound() || program.Matches(in) {
			t.Fatalf("decoded condition evaluated without rebinding: %+v", program)
		}
	}
}

// The policy digest covers the condition: expressions and referenced list
// contents both change the encoding, while list items themselves stay out.
func TestEdgeRuleMatchProgramJSONDistinguishesConditions(t *testing.T) {
	encode := func(raw string, items ...string) string {
		t.Helper()
		var expr EdgeRuleMatchExpr
		if err := json.Unmarshal([]byte(raw), &expr); err != nil {
			t.Fatal(err)
		}
		var lists EdgeRuleLists
		if len(items) > 0 {
			list, err := CompileEdgeRuleList(EdgeRuleListKindIP, items)
			if err != nil {
				t.Fatal(err)
			}
			lists = EdgeRuleLists{"blocked": list}
		}
		program, err := CompileEdgeRuleMatchWithLists(&expr, lists)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(program)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	get := encode(`{"field":"method","op":"eq","value":"GET"}`)
	post := encode(`{"field":"method","op":"eq","value":"POST"}`)
	listed := `{"field":"client_ip","op":"in_list","list":"blocked"}`
	a, b := encode(listed, "203.0.113.0/24"), encode(listed, "198.51.100.0/24")
	if get == post || a == b {
		t.Fatalf("distinct conditions encode identically: %s %s %s %s", get, post, a, b)
	}
	if len(encode(listed, "203.0.113.0/24", "198.51.100.0/24", "192.0.2.0/24")) != len(a) {
		t.Fatal("list items leaked into the encoded condition")
	}
}
