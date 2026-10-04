// adr: 531
package trafficrevocation

import (
	"context"
	"encoding/base64"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestSnapshotRejectsAmbiguousOrUnboundedMetadata(t *testing.T) {
	id := uuid.NewString()
	valid, err := EncodeSnapshot(map[Scope]State{{Kind: "account", ID: id}: {Revision: 1}})
	if err != nil {
		t.Fatal(err)
	}
	jsonValue := func(s string) string { return "v1." + base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for name, value := range map[string]string{
		"version":       "v2." + strings.TrimPrefix(valid, "v1."),
		"oversize":      strings.Repeat("a", api.TrafficSecurityMaxHeaderBytes+1),
		"empty":         jsonValue("[]"),
		"unknown field": jsonValue(`[{"kind":"account","id":"` + id + `","revision":1,"allow":true}]`),
		"duplicate":     jsonValue(`[{"kind":"account","id":"` + id + `","revision":1},{"kind":"account","id":"` + id + `","revision":1}]`),
		"compact id":    jsonValue(`[{"kind":"account","id":"` + strings.ReplaceAll(id, "-", "") + `","revision":1}]`),
		"negative":      jsonValue(`[{"kind":"account","id":"` + id + `","revision":-1}]`),
		"unknown scope": jsonValue(`[{"kind":"node","id":"` + id + `","revision":1}]`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := WithHandoffSnapshot(t.Context(), value); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("invalid metadata accepted: %v", err)
			}
		})
	}
}

func TestSnapshotTrustedIdentityNormalizationAndContextIsolation(t *testing.T) {
	id := uuid.NewString()
	compact := strings.ReplaceAll(id, "-", "")
	states := map[Scope]State{{Kind: "app", ID: compact}: {Revision: 3}}
	if _, err := EncodeSnapshot(states); !errors.Is(err, ErrUnavailable) {
		t.Fatal("untrusted compact id accepted")
	}
	value, err := EncodeAdmittedSnapshot(states)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := WithHandoffSnapshot(t.Context(), value)
	if err != nil {
		t.Fatal(err)
	}
	states[Scope{Kind: "app", ID: compact}] = State{Revision: 7}
	first, ok := HandoffSnapshot(ctx)
	if !ok {
		t.Fatal("handoff absent")
	}
	delete(first, Scope{Kind: "app", ID: id})
	again, _ := HandoffSnapshot(ctx)
	if !maps.Equal(again, map[Scope]State{{Kind: "app", ID: id}: {Revision: 3}}) || HandoffValue(ctx) != value {
		t.Fatal("metadata mutation changed admitted generation")
	}
	if _, exists := HandoffSnapshot(context.Background()); exists {
		t.Fatal("unrelated context acquired a handoff")
	}
	states[Scope{Kind: "app", ID: id}] = State{Revision: 8}
	if _, err := EncodeAdmittedSnapshot(states); !errors.Is(err, ErrUnavailable) {
		t.Fatal("conflicting aliases rebased admission")
	}
	states[Scope{Kind: "app", ID: id}] = State{Revision: 7}
	if _, err := EncodeAdmittedSnapshot(states); err != nil {
		t.Fatalf("equal trusted aliases: %v", err)
	}
}
