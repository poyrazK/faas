package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeBucketLister struct {
	calls int
	items []api.ObjectBucket
}

func (f *fakeBucketLister) ListObjectBuckets(context.Context, string) (api.ObjectBucketList, error) {
	f.calls++
	return api.ObjectBucketList{Items: f.items}, nil
}

// prod 2026-11-19: `bucket upload bh2-svc bh2-files …` printed only the usage
// line; bucket commands took the UUID only.
func TestBucketCommandsAcceptTheBucketName(t *testing.T) {
	const id = "c155a3ee-4e37-4aa2-9889-2f3665047b5d"
	lister := &fakeBucketLister{items: []api.ObjectBucket{{ID: id, Name: "bh2-files"}}}
	client := func() (bucketLister, error) { return lister, nil }
	for _, tc := range []struct{ in, want []string }{
		{[]string{"upload", "bh2-svc", "bh2-files", "a/blob.bin", "blob.bin"}, []string{"upload", "bh2-svc", id, "a/blob.bin", "blob.bin"}},
		{[]string{"sign", "bh2-svc", "bh2-files", "x.html"}, []string{"sign", "bh2-svc", id, "x.html"}},
		{[]string{"deletion", "start", "bh2-svc", "bh2-files", "k", "u"}, []string{"deletion", "start", "bh2-svc", id, "k", "u"}},
		{[]string{"delete", "bh2-svc", id}, []string{"delete", "bh2-svc", id}},
		// Prod hunt r28: today's spellings the tree rewrites to.
		{[]string{"versions", "list", "bh2-svc", "bh2-files"}, []string{"versions", "list", "bh2-svc", id}},
		{[]string{"version-delete", "bh2-svc", "bh2-files", "k", "v"}, []string{"version-delete", "bh2-svc", id, "k", "v"}},
		{[]string{"encryption-keys", "bh2-svc", "bh2-files"}, []string{"encryption-keys", "bh2-svc", id}},
		{[]string{"uploads", "list", "bh2-svc", "bh2-files"}, []string{"uploads", "list", "bh2-svc", id}},
		{[]string{"lifecycle", "get", "bh2-svc", "bh2-files"}, []string{"lifecycle", "get", "bh2-svc", id}},
		{[]string{"list", "bh2-svc"}, []string{"list", "bh2-svc"}},
	} {
		got, err := resolveBucketArgs(context.Background(), tc.in, client)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("resolve %v = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	if _, err := resolveBucketArgs(context.Background(), []string{"sign", "bh2-svc", "missing", "k"}, client); err == nil {
		t.Fatal("an unknown bucket name resolved")
	}
	before := lister.calls
	if _, err := resolveBucketArgs(context.Background(), []string{"delete", "bh2-svc", id}, func() (bucketLister, error) { return nil, errors.New("unused") }); err != nil || lister.calls != before {
		t.Fatalf("an ID was looked up: err=%v calls=%d", err, lister.calls-before)
	}
}
