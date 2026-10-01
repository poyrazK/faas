package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type writeReceiptStore interface {
	gatewayUploadStore
	state.ObjectWriteReceiptStore
	state.ObjectUploadRouteStore
}

// adr: 395
func TestObjectWriteReceiptsMem(t *testing.T) { writeReceiptsSuite(t, state.NewMemStore()) }
func TestObjectWriteReceiptsPG(t *testing.T)  { st, _ := pgStore(t); writeReceiptsSuite(t, st) }

func writeReceiptsSuite(t *testing.T, st writeReceiptStore) {
	ctx := context.Background()
	b, _ := seedAccounting(t, st)
	policy := accountingPolicy()
	var ids []string
	for i := range 6 {
		c := state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "owner-private", Key: "same-key", Bytes: 1, Status: "pending"}
		var err error
		if i == 1 {
			c.SourceKey, c.SourceETag = "private-source", `"private-etag"`
			c, err = st.BeginTrackedGatewayCopy(ctx, c, policy)
		} else {
			c, err = st.BeginTrackedGatewayUpload(ctx, c, policy)
		}
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
		if i < 2 {
			if _, err = st.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID); err != nil {
				t.Fatal(err)
			}
			c.Status, c.ETag = "completed", `"done"`
			if i == 1 {
				c.Status, c.ETag, c.ErrorCode = "failed", "", "provider_write_rejected"
			}
			if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
				t.Fatal(err)
			}
		}
	}
	route, err := st.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Name: "files", MaxBytes: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := st.BeginTrackedObjectUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "api-subject", Key: "route-key", Bytes: 1, Status: "pending"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, c.ID)
	legacy, err := st.RecordObjectUploadCompletion(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), RouteID: route.ID, AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "api-subject", Key: "legacy", Bytes: 1, Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		filter string
		count  int
	}{{"", 5}, {"pending", 5}, {"completed", 1}, {"failed", 1}, {"all", 7}} {
		seen := map[string]bool{}
		cursor := ""
		var previous api.ObjectWriteReceipt
		for {
			page, e := st.ListObjectWriteReceipts(ctx, b.AccountID, b.AppID, b.ID, tc.filter, 2, cursor)
			if e != nil || page.Items == nil || len(page.Items) > 2 {
				t.Fatal(page, e)
			}
			for _, r := range page.Items {
				if seen[r.ID] || previous.ID != "" && (r.CreatedAt.After(previous.CreatedAt) || r.CreatedAt.Equal(previous.CreatedAt) && r.ID >= previous.ID) {
					t.Fatal("unstable pagination", r)
				}
				seen[r.ID], previous = true, r
				if r.Operation == "copy" && (r.Status != "failed" || r.ErrorCode != "provider_write_rejected") {
					t.Fatal(r)
				}
				data, _ := json.Marshal(r)
				for _, private := range []string{"source", "owner-private", "api-subject", "recovery_token", "write_phase", "request_fingerprint"} {
					if strings.Contains(string(data), private) {
						t.Fatal("private receipt projection", string(data))
					}
				}
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor == cursor {
				t.Fatal("cursor cycle")
			}
			cursor = page.NextCursor
		}
		if len(seen) != tc.count {
			t.Fatal(tc.filter, len(seen), tc.count)
		}
	}
	if _, err = st.GetObjectWriteReceipt(ctx, b.AccountID, b.AppID, b.ID, legacy.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("legacy receipt exposed", err)
	}
	for _, identity := range [][3]string{{uuid.NewString(), b.AppID, b.ID}, {b.AccountID, uuid.NewString(), b.ID}, {b.AccountID, b.AppID, uuid.NewString()}} {
		if _, err = st.GetObjectWriteReceipt(ctx, identity[0], identity[1], identity[2], ids[0]); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign get", err)
		}
		if _, err = st.ListObjectWriteReceipts(ctx, identity[0], identity[1], identity[2], "pending", 2, ""); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign list", err)
		}
	}
	page, err := st.ListObjectWriteReceipts(ctx, b.AccountID, b.AppID, b.ID, "pending", 1, "")
	if err != nil || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	for _, tc := range []struct {
		status string
		limit  int
		cursor string
	}{{"invalid", 1, ""}, {"pending", -1, ""}, {"pending", api.ObjectWriteReceiptPageMax + 1, ""}, {"pending", 1, "invalid"}, {"all", 1, page.NextCursor}, {"pending", 1, strings.Repeat("x", api.ObjectWriteReceiptCursorMaxBytes+1)}} {
		if _, err = st.ListObjectWriteReceipts(ctx, b.AccountID, b.AppID, b.ID, tc.status, tc.limit, tc.cursor); !errors.Is(err, state.ErrConflict) {
			t.Fatal(tc, err)
		}
	}
	after, err := st.ObjectUsage(ctx, b.AccountID, before.Reports[0].PeriodStart)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("receipt reads changed accounting", before, after, err)
	}
}
