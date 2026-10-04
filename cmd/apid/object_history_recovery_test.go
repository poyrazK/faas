package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type historicalGatewayObject struct {
	mu                   sync.Mutex
	receipts             []string
	deleted, absent      bool
	lists, writes, heads int
}

func (o *historicalGatewayObject) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		o.writes++
		_, _ = io.Copy(io.Discard, r.Body)
		receipt := r.Header.Get("X-Amz-Meta-" + objectstorage.ReservedUploadReceiptMetadataKey)
		if receipt == "" {
			receipt = r.URL.Query().Get("X-Amz-Meta-" + objectstorage.ReservedUploadReceiptMetadataKey)
		}
		if receipt == "" {
			t.Error("missing provider attempt receipt")
		}
		o.receipts = append(o.receipts, receipt)
		if o.writes == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("ETag", `"new"`)
		w.Header().Set("X-Amz-Version-Id", "v-new")
	case http.MethodDelete:
		o.deleted = true
		w.Header().Set("X-Amz-Delete-Marker", "true")
		w.WriteHeader(http.StatusNoContent)
	case http.MethodHead:
		o.heads++
		if strings.HasSuffix(r.URL.Path, "/source") {
			w.Header().Set("Content-Length", "5")
			w.Header().Set("ETag", `"source"`)
			return
		}
		version := r.URL.Query().Get("versionId")
		if version == "" && o.deleted {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		marker, etag, id := o.receipts[len(o.receipts)-1], `"new"`, "v-new"
		if version == "v-old" {
			marker, etag, id = o.receipts[0], `"old"`, "v-old"
			if o.absent {
				marker = "foreign-receipt"
			}
		}
		if version != "" && version != "v-old" && version != "v-new" {
			t.Error("unexpected version probe", version)
		}
		w.Header().Set("Content-Length", "5")
		w.Header().Set("ETag", etag)
		w.Header().Set("X-Amz-Version-Id", id)
		w.Header().Set("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey, marker)
	case http.MethodGet:
		o.lists++
		q := r.URL.Query()
		if _, ok := q["versions"]; !ok || q.Get("prefix") != "key" || q.Get("max-keys") != fmt.Sprint(api.ObjectUploadHistoryPageSize) {
			t.Error("wrong history inventory", q)
		}
		w.Header().Set("Content-Type", "application/xml")
		if q.Get("key-marker") == "" {
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>true</IsTruncated><NextKeyMarker>key</NextKeyMarker><NextVersionIdMarker>page-new</NextVersionIdMarker><DeleteMarker><Key>key</Key><VersionId>delete-marker</VersionId></DeleteMarker><Version><Key>key</Key><VersionId>v-new</VersionId><Size>5</Size></Version>`)
			for i := range api.ObjectUploadHistoryPageSize - 2 {
				_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>decoy-%d</VersionId><Size>99</Size></Version>`, i)
			}
			_, _ = io.WriteString(w, `</ListVersionsResult>`)
		} else {
			if q.Get("key-marker") != "key" || q.Get("version-id-marker") != "page-new" {
				t.Error("worker restarted scan from beginning", q)
			}
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>key</Key><VersionId>v-old</VersionId><Size>5</Size></Version></ListVersionsResult>`)
		}
	default:
		t.Error("unexpected request", r.Method)
		w.WriteHeader(500)
	}
}

// adr: 539
func TestGatewayHistoricalWriteRecoveryPG(t *testing.T) {
	for _, copyOrigin := range []bool{false, true} {
		for _, mode := range []string{"overwritten", "deleted", "absent"} {
			t.Run(fmt.Sprintf("copy=%t/%s", copyOrigin, mode), func(t *testing.T) {
				ctx := t.Context()
				object := &historicalGatewayObject{absent: mode == "absent"}
				sourceBytes := int64(0)
				if copyOrigin {
					sourceBytes = 5
				}
				f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { object.serve(t, w, r) }), sourceBytes)
				var err error
				if copyOrigin {
					_, err = f.client.CopyObject(ctx, &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("key"), CopySource: aws.String("assets/source")})
				} else {
					_, err = f.client.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("key"), Body: strings.NewReader("hello")})
				}
				if err == nil {
					t.Fatal("lost acknowledgment reported success")
				}
				object.mu.Lock()
				id := object.receipts[0]
				object.mu.Unlock()
				if _, err = f.client.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("key"), Body: strings.NewReader("later")}); err != nil {
					t.Fatal(err)
				}
				object.mu.Lock()
				newID := object.receipts[1]
				object.mu.Unlock()
				ack, err := f.st.GetObjectUploadReceipt(ctx, f.account.ID, f.app.ID, "", f.credential.ID, newID)
				if err != nil || ack.Status != "completed" || !ack.RecoveryVersionsObserved {
					t.Fatal("acknowledged native version did not latch accounting guard", ack, err)
				}
				assertRecoveredObjectEvent(t, f.st, f.bucket, newID, ack.VersionID)
				if mode != "overwritten" {
					if _, err = f.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String("key")}); err == nil {
						t.Fatal("unadmitted delete marker escaped")
					}
					object.mu.Lock()
					object.deleted = true // Retained marker already present at the provider.
					object.mu.Unlock()
				}
				f.enabled.Store(false)
				f.report.ObservedAt = time.Now()
				f.report.CostMillicents = f.policy.MaxMonthlyCostMillicents
				if err = f.st.RecordObjectUsageReport(ctx, f.report); err != nil {
					t.Fatal(err)
				}
				for pass := range 2 {
					// Both the server and store are reconstructed between history pages.
					restarted := state.NewPgStore(f.pool)
					e := setup(t, api.PlanHobby)
					e.s.store = restarted
					e.s.WithObjectStorage(f.registry)
					setS3Flag(t, e, false)
					if _, err = f.pool.Exec(ctx, `UPDATE object_upload_completions SET recovery_retry_at=now()-interval '1 second' WHERE id=$1`, id); err != nil {
						t.Fatal(err)
					}
					if err = e.s.reconcileObjectUploads(ctx, nil); err != nil {
						t.Fatal(err)
					}
					c, err := restarted.GetObjectUploadReceipt(ctx, f.account.ID, f.app.ID, "", f.credential.ID, id)
					if err != nil || !c.RecoveryVersionsObserved {
						t.Fatal(c, err)
					}
					want := "pending"
					if pass == 1 && mode != "absent" {
						want = "completed"
					}
					if c.Status != want || pass == 0 && c.RecoveryCursor == "" || pass == 1 && c.RecoveryCursor != "" {
						t.Fatal("incorrect recovery progress", pass, c)
					}
					if want == "completed" && c.ETag != `"old"` {
						t.Fatal("new object falsely proved old receipt", c)
					}
					if want == "completed" {
						if native, resolveErr := restarted.ResolveObjectVersion(ctx, f.account.ID, f.bucket.ID, "key", c.VersionID); resolveErr != nil || native != "v-old" {
							t.Fatal("recovered selector lost exact proof", native, resolveErr)
						}
						assertRecoveredObjectEvent(t, restarted, f.bucket, id, c.VersionID)
					} else if pendingEvent, claimErr := restarted.ClaimDuePublishedEvent(ctx, time.Now().Add(time.Second)); !errors.Is(claimErr, state.ErrNotFound) {
						t.Fatal("uncertain history published creation", pendingEvent, claimErr)
					}

				}
				object.mu.Lock()
				writes, lists, requests := object.writes, object.lists, object.writes+object.lists+object.heads
				object.mu.Unlock()
				if writes != 2 || lists != 2 {
					t.Fatal("recovery replayed a write or pagination", writes, lists)
				}
				metrics, err := f.st.ListObjectStorageProviderRequestMetrics(ctx, f.bucket.BackendID, f.bucket.BackendFingerprint, state.ObjectStoragePeriod(time.Now()))
				if err != nil || len(metrics) != 1 || metrics[0].RequestCount != int64(requests) {
					t.Fatal("unmetered native recovery calls", requests, metrics, err)
				}
				j, err := f.st.RequestObjectCapacityReconciliation(ctx, f.account.ID, f.app.ID, f.bucket.ID)
				if err != nil {
					t.Fatal(err)
				}
				j, err = f.st.ClaimObjectCapacityReconciliation(ctx, j.ID, "capacity")
				wantState := "scanning"
				if mode == "absent" {
					wantState = "waiting"
				}
				if err != nil || j.State != wantState || j.InventoryScope != state.ObjectInventoryAllVersions || j.ReclaimedBytes != 0 {
					t.Fatal("versions refunded by current inventory", j, err)
				}
				usage, err := f.st.ObjectUsage(ctx, f.account.ID, time.Now())
				wantAuth := int64(2)
				if copyOrigin {
					wantAuth = 3
				}
				if err != nil || usage.Authorizations != wantAuth || usage.Reports[0].CostMillicents != f.policy.MaxMonthlyCostMillicents {
					t.Fatal("recovery changed monthly safety ledger", usage, err)
				}
				operation := "put"
				if copyOrigin {
					operation = "copy"
				}
				pollGatewayOperationReceipt(t, f, "key", id, map[bool]string{true: "pending", false: "completed"}[mode == "absent"], operation, 200)
			})
		}
	}
}

func assertRecoveredObjectEvent(t *testing.T, st *state.PgStore, b state.ObjectBucket, id, version string) {
	t.Helper()
	work, err := st.ClaimDuePublishedEvent(t.Context(), time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var envelope events.Envelope
	if err = json.Unmarshal(work.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if err = envelope.Validate(); err != nil {
		t.Fatal(err)
	}
	var data api.ObjectStorageEvent
	if err = json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	if envelope.ID != "write:"+id || envelope.Source != api.ObjectEventSource || envelope.Type != api.ObjectEventCreated || data.VersionID != version || !state.ValidObjectVersionID(version) || data.BucketID != b.ID || data.ReceiptID != id {
		t.Fatal(envelope, data)
	}
	if strings.Contains(string(work.Payload), "v-old") || strings.Contains(string(work.Payload), "v-new") {
		t.Fatal("native proof exposed", string(work.Payload))
	}
	if err = st.FinishPublishedEvent(t.Context(), work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
}
