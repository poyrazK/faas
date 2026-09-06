package storage

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestOCISnapshotCaptureRoundTripAndIsolation(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	ctx := context.Background()
	dep := "550e8400-e29b-41d4-a716-446655440000"
	captures := []string{"660e8400-e29b-41d4-a716-446655440001", "770e8400-e29b-41d4-a716-446655440002"}
	keys := []string{}
	for _, tier := range []string{"", "warm/"} {
		for _, part := range []string{"mem", "vmstate"} {
			keys = append(keys, "snap/"+dep+"/"+tier+part)
			for _, capture := range captures {
				keys = append(keys, "snap/"+dep+"/"+tier+"captures/"+capture+"/"+part)
			}
		}
	}
	for _, key := range keys {
		if err := be.Put(ctx, key, strings.NewReader("content:"+key)); err != nil {
			t.Fatalf("Put(%s): %v", key, err)
		}
	}
	// Each capture, tier, and device-state sibling must retain its own bytes.
	for _, key := range keys {
		r, err := be.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil || !bytes.Equal(body, []byte("content:"+key)) {
			t.Fatalf("Get(%s) content=%q err=%v", key, body, err)
		}
		repo, tag, err := be.plan(key)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := be.unplan(repo, tag); !ok || got != key {
			t.Fatalf("plan/unplan(%s)=%s,%v", key, got, ok)
		}
	}
	got, err := be.List(ctx, "snap/"+dep+"/")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, key := range got {
		seen[key] = true
	}
	if len(seen) != len(keys) {
		t.Fatalf("List returned %d keys, want %d: %v", len(seen), len(keys), got)
	}
	for _, key := range keys {
		if !seen[key] {
			t.Fatalf("List missing %s", key)
		}
	}
	deleted := keys[1]
	if err := be.Delete(ctx, deleted); err != nil {
		t.Fatal(err)
	}
	if r, err := be.Get(ctx, deleted); !IsNotFound(err) {
		if r != nil {
			_ = r.Close()
		}
		t.Fatalf("deleted capture still readable: %v", err)
	}
	for _, key := range keys {
		if key == deleted {
			continue
		}
		r, err := be.Get(ctx, key)
		if err != nil {
			t.Fatalf("deleting one capture affected %s: %v", key, err)
		}
		_ = r.Close()
	}
}

func TestOCISnapshotCaptureRejectsInvalidKeys(t *testing.T) {
	be := &OCIRegistryStorageBackend{}
	dep := "550e8400-e29b-41d4-a716-446655440000"
	cap := "660e8400-e29b-41d4-a716-446655440001"
	for _, suffix := range []string{"captures/not-a-uuid/mem", "captures/" + cap + "/bogus", "warm/captures/" + cap + "/mem/extra", "cold/captures/" + cap + "/mem", "captures//mem", "warm/../mem"} {
		if _, _, err := be.plan("snap/" + dep + "/" + suffix); !IsInvalidKey(err) {
			t.Fatalf("accepted %q: %v", suffix, err)
		}
	}
	for _, tag := range []string{"warm-bogus", "captures-invalid-mem", "captures-" + cap + "-bogus", "warm-captures-" + cap + "-mem-extra"} {
		if key, ok := be.unplan("snap-"+dep, tag); ok {
			t.Fatalf("accepted invalid tag %q as %q", tag, key)
		}
	}
}

func TestOCISnapshotCaptureDeletePreservesIdenticalSibling(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	be := f.client(t)
	ctx := context.Background()
	prefix := "snap/550e8400-e29b-41d4-a716-446655440000/captures/"
	keys := []string{prefix + "660e8400-e29b-41d4-a716-446655440001/mem", prefix + "770e8400-e29b-41d4-a716-446655440002/mem"}
	for _, key := range keys {
		if err := be.Put(ctx, key, strings.NewReader("identical memory")); err != nil {
			t.Fatal(err)
		}
	}
	if err := be.Delete(ctx, keys[0]); err != nil {
		t.Fatal(err)
	}
	r, err := be.Get(ctx, keys[1])
	if err != nil {
		t.Fatalf("deleting one capture removed identical sibling: %v", err)
	}
	defer r.Close()
	body, err := io.ReadAll(r)
	if err != nil || string(body) != "identical memory" {
		t.Fatalf("sibling content=%q err=%v", body, err)
	}
}
