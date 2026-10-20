// adr: 1197
package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const signalTestOperation = "11111111-1111-1111-1111-111111111111"

type signalTestClient struct {
	single  []api.OperationSignalRequest
	subject []api.OperationSubjectSignalRequest
}

func (f *signalTestClient) SignalOperation(_ context.Context, app, id string, req api.OperationSignalRequest) (api.OperationSignalReceipt, error) {
	f.single = append(f.single, req)
	return api.OperationSignalReceipt{OperationID: id, SignalID: req.SignalID, Event: req.Event, Waiting: true}, nil
}

func (f *signalTestClient) SignalOperationsBySubject(_ context.Context, app string, req api.OperationSubjectSignalRequest) (api.OperationSubjectSignalResponse, error) {
	f.subject = append(f.subject, req)
	if req.Subject.ID == "none" {
		return api.OperationSubjectSignalResponse{Delivered: []api.OperationSignalReceipt{}}, nil
	}
	return api.OperationSubjectSignalResponse{Delivered: []api.OperationSignalReceipt{{OperationID: signalTestOperation, SignalID: req.SignalID, Event: req.Event, Duplicate: true}}}, nil
}

func TestCustomerOperationSignal(t *testing.T) {
	client := &signalTestClient{}
	c, err := main.ParseCustomerOperationSignal([]string{signalTestOperation, "--app", "orders", "--event", "payment.succeeded", "--signal-id", "evt_1", "--payload", `{"charge":"ch_1"}`})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := main.RunCustomerOperationSignal(context.Background(), client, c, &out, false); err != nil {
		t.Fatal(err)
	}
	if len(client.single) != 1 || string(client.single[0].Payload) != `{"charge":"ch_1"}` || !strings.Contains(out.String(), "resumed its wait") {
		t.Fatalf("single signal %+v -> %q", client.single, out.String())
	}
	c, err = main.ParseCustomerOperationSignal([]string{"--app", "orders", "--scope", "production", "--operation", "fulfil", "--subject", "order:ord_1", "--event", "payment.succeeded", "--signal-id", "evt_1"})
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := main.RunCustomerOperationSignal(context.Background(), client, c, &out, false); err != nil {
		t.Fatal(err)
	}
	if len(client.subject) != 1 || client.subject[0].Subject != (api.OperationSubject{Type: "order", ID: "ord_1"}) || !strings.Contains(out.String(), "already delivered") {
		t.Fatalf("subject signal %+v -> %q", client.subject, out.String())
	}
	(*c.Subject()) = "order:none"
	out.Reset()
	if err := main.RunCustomerOperationSignal(context.Background(), client, c, &out, false); err != nil || !strings.Contains(out.String(), "nothing was delivered") {
		t.Fatalf("unmatched subject %q %v", out.String(), err)
	}
}

func TestParseCustomerOperationSignalRejectsAmbiguousTargets(t *testing.T) {
	base := []string{"--app", "orders", "--event", "payment.succeeded", "--signal-id", "evt_1"}
	for name, args := range map[string][]string{
		"no target":         base,
		"both targets":      append([]string{signalTestOperation, "--scope", "production", "--operation", "fulfil", "--subject", "order:1"}, base...),
		"partial subject":   append([]string{"--scope", "production", "--subject", "order:1"}, base...),
		"subject sans type": append([]string{"--scope", "production", "--operation", "fulfil", "--subject", "ord_1"}, base...),
		"no signal id":      {signalTestOperation, "--app", "orders", "--event", "payment.succeeded"},
		"invalid payload":   append([]string{signalTestOperation, "--payload", "{"}, base...),
	} {
		if _, err := main.ParseCustomerOperationSignal(args); err == nil {
			t.Fatalf("%s: accepted %v", name, args)
		}
	}
}

// ADR-20261017100853: --self reaches only the tenant-self routes, and a subject signal
// sends the definition instead of an app, scope and operation name.
func TestCustomerOperationSignalSelfUsesTenantRoutes(t *testing.T) {
	const definition = "22222222-2222-2222-2222-222222222222"
	var paths []string
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if strings.HasSuffix(r.URL.Path, "/customer-operation-signals") {
			_, _ = w.Write([]byte(`{"delivered":[{"operation_id":"` + signalTestOperation + `","signal_id":"evt_1","event":"payment.succeeded","duplicate":false,"waiting":true}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"operation_id":"` + signalTestOperation + `","signal_id":"evt_1","event":"payment.succeeded","duplicate":false,"waiting":true}`))
	}))
	defer server.Close()
	client := tenantCustomerOperationSignalClient{client: api.NewClient(server.URL, "tenant-token").SetCompletionCache(nil), definition: definition}
	for _, args := range [][]string{
		{signalTestOperation, "--self", "--event", "payment.succeeded", "--signal-id", "evt_1"},
		{"--self", "--definition", definition, "--subject", "order:ord_1", "--event", "payment.succeeded", "--signal-id", "evt_1"},
	} {
		c, err := parseCustomerOperationSignal(args)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := runCustomerOperationSignal(context.Background(), client, c, &out, false); err != nil || !strings.Contains(out.String(), "resumed its wait") {
			t.Fatalf("%v: %q %v", args, out.String(), err)
		}
	}
	want := []string{"POST /v1/platform-tenant-self/customer-operations/" + signalTestOperation + "/signals", "POST /v1/platform-tenant-self/customer-operation-signals"}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths %v", paths)
	}
	if bodies[1]["definition_id"] != definition || bodies[1]["scope"] != nil || bodies[1]["operation"] != nil {
		t.Fatalf("subject body %v", bodies[1])
	}
}

func TestParseCustomerOperationSignalSelfRules(t *testing.T) {
	const definition = "22222222-2222-2222-2222-222222222222"
	base := []string{"--event", "payment.succeeded", "--signal-id", "evt_1"}
	for name, args := range map[string][]string{
		"self with app":           append([]string{signalTestOperation, "--self", "--app", "orders"}, base...),
		"self with scope":         append([]string{"--self", "--scope", "production", "--definition", definition, "--subject", "order:1"}, base...),
		"self subject sans def":   append([]string{"--self", "--subject", "order:1"}, base...),
		"self bad definition":     append([]string{"--self", "--definition", "fulfil", "--subject", "order:1"}, base...),
		"definition without self": append([]string{"--app", "orders", "--definition", definition, "--subject", "order:1"}, base...),
		"account without app":     append([]string{signalTestOperation}, base...),
	} {
		if _, err := parseCustomerOperationSignal(args); err == nil {
			t.Fatalf("%s: accepted %v", name, args)
		}
	}
}
