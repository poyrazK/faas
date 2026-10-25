// adr: 20261022184531
package grpcerr_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
)

// The wake stage travels beside the Problem: the Problem round-trips
// unchanged, and its own fields win over a colliding extra key.
func TestToStatusWithMetadata(t *testing.T) {
	limit := int64(3)
	p := &api.Problem{Code: api.CodeCapacity, Title: "no jail uid", Detail: "acquire lease: exhausted", Limit: &limit}
	err := grpcerr.ToStatusWithMetadata(p, map[string]string{grpcerr.MetadataWakeStage: grpcerr.WakeStagePreStart, "title": "spoofed"})
	if stage, ok := grpcerr.MetadataValue(err, grpcerr.MetadataWakeStage); !ok || stage != grpcerr.WakeStagePreStart {
		t.Fatalf("wake stage = %q, %t", stage, ok)
	}
	got, ok := grpcerr.FromStatus(err)
	if !ok || got.Code != p.Code || got.Title != p.Title || got.Detail != p.Detail || got.Limit == nil || *got.Limit != limit {
		t.Fatalf("round trip = %+v", got)
	}
	if _, ok := grpcerr.MetadataValue(grpcerr.ToStatus(p), grpcerr.MetadataWakeStage); ok {
		t.Fatal("ToStatus added a wake stage")
	}
	if _, ok := grpcerr.MetadataValue(errors.New("plain"), grpcerr.MetadataWakeStage); ok {
		t.Fatal("a plain error carried metadata")
	}
}
