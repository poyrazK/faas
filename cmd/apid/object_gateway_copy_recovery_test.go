package main

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type acceptedGatewayCopy struct {
	mu                  sync.Mutex
	receipt             string
	copies              int
	source, destination bool
	changeSource        bool
}

func (o *acceptedGatewayCopy) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	source := strings.HasSuffix(r.URL.Path, "/source")
	switch r.Method {
	case http.MethodHead:
		if source && !o.source || !source && !o.destination {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Length", "5")
		if source {
			w.Header().Set("ETag", `"source"`)
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("X-Amz-Meta-Owner", "customer")
			w.Header().Set("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey, "old-source-receipt")
		} else {
			w.Header().Set("ETag", `"copied"`)
			w.Header().Set("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey, o.receipt)
		}
	case http.MethodPut:
		o.copies++
		if r.Header.Get("X-Amz-Copy-Source-If-Match") != `"source"` || r.Header.Get("X-Amz-Metadata-Directive") != "REPLACE" || r.Header.Get("X-Amz-Meta-Owner") != "customer" || r.Header.Get("Content-Type") != "image/png" {
			t.Error("copy not bound to measured source or metadata")
		}
		o.receipt = r.Header.Get("X-Amz-Meta-" + objectstorage.ReservedUploadReceiptMetadataKey)
		if o.receipt == "" || o.receipt == "old-source-receipt" {
			t.Error("source receipt propagated to destination")
		}
		if o.changeSource {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(412)
			_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code></Error>`)
			return
		}
		o.destination = true
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	case http.MethodDelete:
		if source {
			o.source = false
		} else {
			o.destination = false
		}
		w.WriteHeader(204)
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Has("versioning") {
			_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
			return
		}
		if o.source || o.destination {
			t.Error("inventory ran before deletion")
		}
		_, _ = io.WriteString(w, `<ListBucketResult><Name>physical</Name><IsTruncated>false</IsTruncated><KeyCount>0</KeyCount></ListBucketResult>`)
	default:
		t.Error("unexpected provider request", r.Method)
		w.WriteHeader(500)
	}
}

// adr: 394
// adr: 405
func TestGatewayCopyRecoveryPG(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(strconv.FormatBool(changed), func(t *testing.T) {
			ctx := t.Context()
			object := &acceptedGatewayCopy{source: true, changeSource: changed}
			f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { object.serve(t, w, r) }), 5)
			_, err := f.client.CopyObject(ctx, &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("destination"), CopySource: aws.String("assets/source")})
			if err == nil {
				t.Fatal("unconfirmed copy reported success")
			}
			object.mu.Lock()
			id, copies, destination := object.receipt, object.copies, object.destination
			object.mu.Unlock()
			if copies != 1 || destination == changed {
				t.Fatal("copy replay or source mutation copied", copies, destination)
			}
			c, err := f.st.GetObjectUploadReceipt(ctx, f.account.ID, f.app.ID, "", f.credential.ID, id)
			if err != nil || c.Origin != "gateway_copy" || c.SourceKey != "source" || c.SourceETag != `"source"` || c.Bytes != 5 {
				t.Fatal(c, err)
			}
			if changed {
				if c.Status != "failed" || c.WritePhase != state.ObjectUploadSettled {
					t.Fatal("source change not settled", c)
				}
				pollGatewayWriteReceipt(t, f, "destination", id, "failed", 200)
				return
			}
			if c.Status != "pending" || c.WritePhase != state.ObjectUploadDispatched {
				t.Fatal(c)
			}
			restarted := state.NewPgStore(f.pool)
			e := setup(t, api.PlanHobby)
			e.s.store = restarted
			e.s.WithObjectStorage(f.registry)
			setS3Flag(t, e, false)
			f.report.ObservedAt = time.Now()
			f.report.CostMillicents = f.policy.MaxMonthlyCostMillicents
			if err = restarted.RecordObjectUsageReport(ctx, f.report); err != nil {
				t.Fatal(err)
			}
			f.enabled.Store(false)
			pollGatewayWriteReceipt(t, f, "destination", id, "pending", 200)
			// Pending copies fence current deletion. Recovery still relies only on
			// destination proof if the source disappears outside the gateway.
			if _, err = f.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String("source")}); err == nil {
				t.Fatal("current deletion escaped an uncertain copy's fence")
			}
			object.mu.Lock()
			sourceExists := object.source
			object.source = false
			object.mu.Unlock()
			if !sourceExists {
				t.Fatal("fenced deletion reached the provider")
			}
			if _, err = f.pool.Exec(ctx, `UPDATE object_upload_completions SET recovery_retry_at=now()-interval '1 second' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			operations := []string{}
			if err = e.s.reconcileObjectUploads(ctx, func(operation, outcome string) { operations = append(operations, operation+"/"+outcome) }); err != nil {
				t.Fatal(err)
			}
			c, err = restarted.GetObjectUploadReceipt(ctx, f.account.ID, f.app.ID, "", f.credential.ID, id)
			if err != nil || c.Status != "completed" || c.ETag != `"copied"` || len(operations) != 1 || operations[0] != "gateway_copy/completed" {
				t.Fatal(c, operations, err)
			}
			pollGatewayWriteReceipt(t, f, "destination", id, "completed", 200)
			object.mu.Lock()
			copies = object.copies
			object.mu.Unlock()
			if copies != 1 {
				t.Fatal("recovery replayed copy", copies)
			}
			for _, key := range []string{"destination"} {
				if _, err = f.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(key)}); err != nil {
					t.Fatal(err)
				}
			}
			j, err := restarted.RequestObjectCapacityReconciliation(ctx, f.account.ID, f.app.ID, f.bucket.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.s.reconcileObjectCapacity(ctx, nil); err != nil {
				t.Fatal(err)
			}
			j, err = restarted.GetObjectCapacityReconciliation(ctx, f.account.ID, f.bucket.ID, j.ID)
			if err != nil || j.State != "completed" || j.ReclaimedBytes != 10 {
				t.Fatal(j, err)
			}
			usage, err := restarted.ObjectUsage(ctx, f.account.ID, time.Now())
			if err != nil || usage.Authorizations != 2 || usage.Reports[0].CostMillicents != f.policy.MaxMonthlyCostMillicents {
				t.Fatal("copy recovery changed monthly ledger", usage, err)
			}
			f.registry.Accounting.MaxMonthlyCostMillicents *= 2
			_, err = restarted.BeginTrackedGatewayCopy(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: f.account.ID, AppID: f.app.ID, BucketID: f.bucket.ID, SubjectID: f.credential.ID, Key: "reuse", Bytes: 100, Status: "pending", SourceKey: "new-source", SourceETag: `"new-source"`}, f.registry.Accounting)
			if err != nil {
				t.Fatal("copy capacity not reusable", err)
			}
			if err = restarted.RevokeObjectS3Credential(ctx, f.account.ID, f.bucket.ID, f.credential.ID); err != nil {
				t.Fatal(err)
			}
			pollGatewayWriteReceipt(t, f, "destination", id, "", 403)
		})
	}
}
