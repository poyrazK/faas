package gateway

import (
	"errors"
	"fmt"
	"maps"
	"testing"

	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

func trafficSnapshotForTest(t *testing.T, revision int64) string {
	t.Helper()
	value, err := trafficrevocation.EncodeSnapshot(map[trafficrevocation.Scope]trafficrevocation.State{
		{Kind: "app", ID: "6f1c2a3b-4d5e-4f60-8a7b-9c0d1e2f3a4b"}:     {Revision: revision},
		{Kind: "account", ID: "0a1b2c3d-4e5f-4a6b-8c7d-8e9f0a1b2c3d"}: {Revision: 1},
	})
	if err != nil {
		t.Fatalf("EncodeSnapshot: %v", err)
	}
	return value
}

func TestTrafficSnapshotMemoMatchesDecode(t *testing.T) {
	valid := trafficSnapshotForTest(t, 7)
	for _, tc := range []struct {
		name  string
		memo  *trafficSnapshotMemo
		value string
	}{
		{name: "memo valid", memo: &trafficSnapshotMemo{}, value: valid},
		{name: "nil memo valid", memo: nil, value: valid},
		{name: "memo invalid", memo: &trafficSnapshotMemo{}, value: "v1.not-base64!"},
		{name: "memo non-canonical", memo: &trafficSnapshotMemo{}, value: valid + "A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, wantErr := trafficrevocation.DecodeSnapshot(tc.value)
			for range 2 { // the second call is served from the memo
				got, err := tc.memo.decode(tc.value)
				if !errors.Is(err, wantErr) || !maps.Equal(got, want) {
					t.Fatalf("decode = %v, %v; want %v, %v", got, err, want, wantErr)
				}
			}
			if wantErr != nil && tc.memo != nil && len(tc.memo.current)+len(tc.memo.previous) != 0 {
				t.Fatalf("memo kept a value that failed to decode")
			}
		})
	}
}

func TestTrafficSnapshotMemoIsBounded(t *testing.T) {
	memo := &trafficSnapshotMemo{}
	hot := trafficSnapshotForTest(t, 0)
	if _, err := memo.decode(hot); err != nil {
		t.Fatalf("decode hot: %v", err)
	}
	for i := 1; i <= 3*trafficSnapshotMemoEntries; i++ {
		if _, err := memo.decode(trafficSnapshotForTest(t, int64(i))); err != nil {
			t.Fatalf("decode %d: %v", i, err)
		}
		if i%(trafficSnapshotMemoEntries/2) == 0 {
			if _, err := memo.decode(hot); err != nil { // keeps hot in use
				t.Fatalf("decode hot: %v", err)
			}
		}
		if held := len(memo.current) + len(memo.previous); held > 2*trafficSnapshotMemoEntries {
			t.Fatalf("memo holds %d values, want at most %d", held, 2*trafficSnapshotMemoEntries)
		}
	}
	if _, ok := memo.current[hot]; !ok {
		if _, ok := memo.previous[hot]; !ok {
			t.Fatalf("a value in use aged out of the memo")
		}
	}
	if _, ok := memo.current[trafficSnapshotForTest(t, 1)]; ok {
		t.Fatalf("a stale value is still in the current generation")
	}
}

func BenchmarkTrafficSnapshotDecode(b *testing.B) {
	value, err := trafficrevocation.EncodeSnapshot(map[trafficrevocation.Scope]trafficrevocation.State{
		{Kind: "app", ID: "6f1c2a3b-4d5e-4f60-8a7b-9c0d1e2f3a4b"}:        {Revision: 3},
		{Kind: "account", ID: "0a1b2c3d-4e5f-4a6b-8c7d-8e9f0a1b2c3d"}:    {Revision: 1},
		{Kind: "deployment", ID: "1b2c3d4e-5f60-4a7b-8c9d-0e1f2a3b4c5d"}: {Revision: 2},
	})
	if err != nil {
		b.Fatal(err)
	}
	for _, memo := range []*trafficSnapshotMemo{nil, {}} {
		b.Run(fmt.Sprintf("memo=%t", memo != nil), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := memo.decode(value); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
