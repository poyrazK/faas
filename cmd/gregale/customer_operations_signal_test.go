// adr: 1197
package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

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
	c, err := parseCustomerOperationSignal([]string{signalTestOperation, "--app", "orders", "--event", "payment.succeeded", "--signal-id", "evt_1", "--payload", `{"charge":"ch_1"}`})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runCustomerOperationSignal(context.Background(), client, c, &out, false); err != nil {
		t.Fatal(err)
	}
	if len(client.single) != 1 || string(client.single[0].Payload) != `{"charge":"ch_1"}` || !strings.Contains(out.String(), "resumed its wait") {
		t.Fatalf("single signal %+v -> %q", client.single, out.String())
	}
	c, err = parseCustomerOperationSignal([]string{"--app", "orders", "--scope", "production", "--operation", "fulfil", "--subject", "order:ord_1", "--event", "payment.succeeded", "--signal-id", "evt_1"})
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runCustomerOperationSignal(context.Background(), client, c, &out, false); err != nil {
		t.Fatal(err)
	}
	if len(client.subject) != 1 || client.subject[0].Subject != (api.OperationSubject{Type: "order", ID: "ord_1"}) || !strings.Contains(out.String(), "already delivered") {
		t.Fatalf("subject signal %+v -> %q", client.subject, out.String())
	}
	c.subject = "order:none"
	out.Reset()
	if err := runCustomerOperationSignal(context.Background(), client, c, &out, false); err != nil || !strings.Contains(out.String(), "nothing was delivered") {
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
		if _, err := parseCustomerOperationSignal(args); err == nil {
			t.Fatalf("%s: accepted %v", name, args)
		}
	}
}
