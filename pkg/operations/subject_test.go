// ADR-639: bounded immutable business correlation metadata.
package operations

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOperationSubjectPointersAndBounds(t *testing.T) {
	for _, tc := range []struct{ name, pointer, input, want string }{
		{"field", "/order_id", `{"order_id":"ord-123"}`, "ord-123"},
		{"escaped", "/a~1b/~0key", `{"a/b":{"~key":"é: 42"}}`, "é: 42"},
		{"array", "/orders/0/id", `{"orders":[{"id":"first"}]}`, "first"},
		{"empty key", "/", `{"":"empty-key"}`, "empty-key"},
		{"byte bound", "/id", `{"id":` + string(mustSubjectJSON(t, strings.Repeat("é", api.OperationSubjectIDMaxBytes/2))) + `}`, strings.Repeat("é", api.OperationSubjectIDMaxBytes/2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := ExtractOperationSubject(&api.OperationSubjectSpec{Type: "order", IDFrom: tc.pointer}, []byte(tc.input))
			if err != nil || ref == nil || ref.Type != "order" || ref.ID != tc.want {
				t.Fatalf("reference: %+v %v", ref, err)
			}
		})
	}
	for _, input := range []string{`{}`, `{"id":null}`, `{"id":1}`, `{"id":{}}`, `{"id":[]}`, `{"id":""}`, `{"id":"a\n"}`, `{"id":"a\u007f"}`, `{"id":"a"} {}`, `{"id":` + string(mustSubjectJSON(t, strings.Repeat("é", 129))) + `}`} {
		if _, err := ExtractOperationSubject(&api.OperationSubjectSpec{Type: "order", IDFrom: "/id"}, []byte(input)); err == nil {
			t.Fatalf("accepted invalid input %.70s", input)
		}
	}
	for _, pointer := range []string{"/orders/01/id", "/orders/-/id", "/orders/-1/id", "/orders/1/id"} {
		if _, err := ExtractOperationSubject(&api.OperationSubjectSpec{Type: "order", IDFrom: pointer}, []byte(`{"orders":[{"id":"first"}]}`)); err == nil {
			t.Fatalf("accepted array index %q", pointer)
		}
	}
	if ref, err := ExtractOperationSubject(nil, []byte(`{"id":123}`)); err != nil || ref != nil {
		t.Fatalf("legacy reference: %+v %v", ref, err)
	}
}

func mustSubjectJSON(t *testing.T, value string) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestOperationSubjectDeclarationRevisions(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanPro).Operations
	legacy, err := Compile(testSpec(), limits)
	if err != nil {
		t.Fatal(err)
	}
	spec := testSpec()
	spec.Subject = &api.OperationSubjectSpec{Type: "order", IDFrom: "/count"}
	declared, err := Compile(spec, limits)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Revision == declared.Revision {
		t.Fatal("subject not pinned to definition revision")
	}
	spec.Subject.Type = "changed"
	if declared.Spec.Subject.Type != "order" {
		t.Fatal("caller mutated compiled declaration")
	}
	for _, subject := range []api.OperationSubjectSpec{
		{Type: "Order", IDFrom: "/id"}, {Type: "", IDFrom: "/id"}, {Type: strings.Repeat("a", 65), IDFrom: "/id"},
		{Type: "order", IDFrom: ""}, {Type: "order", IDFrom: "id"}, {Type: "order", IDFrom: "#/%69d"},
		{Type: "order", IDFrom: "/~2id"}, {Type: "order", IDFrom: "/~"}, {Type: "order", IDFrom: "/id\t"},
		{Type: "order", IDFrom: "/" + strings.Repeat("a", api.OperationPathMaxBytes)}, {Type: "order", IDFrom: "/\xff"},
	} {
		invalid := testSpec()
		invalid.Subject = &subject
		if _, err := Compile(invalid, limits); err == nil {
			t.Fatalf("accepted invalid declaration %+v", subject)
		}
	}
}
