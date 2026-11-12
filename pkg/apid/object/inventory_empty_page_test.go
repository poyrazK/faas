package object

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type emptyPageInventoryLister struct {
	objectstorage.Provider
	calls, reservations int
	fault               string
}

func (p *emptyPageInventoryLister) ListObjects(_ context.Context, _, _, cursor string, _ int32) (objectstorage.ObjectPage, error) {
	p.calls++
	if p.calls != p.reservations {
		return objectstorage.ObjectPage{}, errors.New("native page was not reserved")
	}
	if cursor == "" || p.fault == "cycle" {
		return objectstorage.ObjectPage{NextCursor: "native-next"}, nil
	}
	if p.fault == "failure" {
		return objectstorage.ObjectPage{}, objectstorage.ErrUnavailable
	}
	return objectstorage.ObjectPage{Items: []objectstorage.Object{{Key: "key", Size: 7}}}, nil
}

func (p *emptyPageInventoryLister) ListObjectsV2(ctx context.Context, bucket string, r objectstorage.ObjectListRequest) (objectstorage.ObjectPage, error) {
	return p.ListObjects(ctx, bucket, r.Prefix, r.Cursor, r.Limit)
}

func TestInventoriesFollowEmptyNativeContinuations(t *testing.T) {
	for _, resumable := range []bool{false, true} {
		for _, fault := range []string{"none", "failure", "cycle"} {
			t.Run(fault+map[bool]string{false: "/complete", true: "/resumable"}[resumable], func(t *testing.T) {
				p := &emptyPageInventoryLister{fault: fault}
				record := func(context.Context) error { p.reservations++; return nil }
				var bytes, keys int64
				var err error
				var scan state.ObjectInventoryScan
				if resumable {
					err = ResumeObjectInventory(t.Context(), p, "physical", &scan, record)
					bytes, keys = scan.Bytes, scan.Keys
				} else {
					bytes, keys, err = CompleteObjectInventory(t.Context(), p, "physical", record)
				}
				if p.calls != 2 || p.reservations != 2 {
					t.Fatal("empty page continuation skipped admission", p.calls, p.reservations, err)
				}
				if fault == "none" {
					if err != nil || bytes != 7 || keys != 1 || resumable && !scan.Complete {
						t.Fatal("inventory stopped at an empty native page", scan, bytes, keys, err)
					}
				} else if err == nil || scan.Complete || bytes != 0 || keys != 0 {
					t.Fatal("incomplete listing became a complete empty inventory", scan, bytes, keys, err)
				}
			})
		}
	}
}
