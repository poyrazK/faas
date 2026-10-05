package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 599
func TestS3ProtectedEventWriteReadback(t *testing.T) {
	for _, policy := range []string{"explicit-days", "explicit-years", "default", "off-fixed"} {
		for _, bad := range []string{"", "missing-event", "duplicate-event", "wrong-status", "wrong-duration", "both-durations", "missing-date", "short-date", "unknown-header"} {
			t.Run(policy+"/"+bad, func(t *testing.T) {
				snapshot := testWriteProtection()
				days, years := int32(30), int32(1)
				switch policy {
				case "explicit-days":
					snapshot.Requested.Retention = &api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &days}}
				case "explicit-years":
					snapshot.Requested.Retention = &api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Years: &years}}
				case "default":
					snapshot.Requested.Retention = nil
					snapshot.DefaultRetention = &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", DefaultEventHold: &api.ObjectRetentionPeriod{Days: &days}}
				case "off-fixed":
					snapshot.Requested.Retention.EventHold = "OFF"
				}
				receipt := uuid.NewString()
				puts, heads := 0, 0
				provider := protectionTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "PUT" {
						puts++
						expected := snapshot.Requested.Retention
						if expected == nil {
							if r.Header.Get("X-Amz-Object-Lock-Event-Hold") != "" {
								t.Error("default replaced by explicit hold")
							}
						} else if r.Header.Get("X-Amz-Object-Lock-Event-Hold") != expected.EventHold {
							t.Error("event policy lost")
						}
						wantDays, wantYears := "", ""
						if expected != nil && expected.EventHoldDuration != nil {
							if d := expected.EventHoldDuration.Days; d != nil {
								wantDays = strconv.FormatInt(int64(*d), 10)
							}
							if y := expected.EventHoldDuration.Years; y != nil {
								wantYears = strconv.FormatInt(int64(*y), 10)
							}
						}
						if r.Header.Get("X-Amz-Object-Lock-Event-Hold-Duration-Days") != wantDays || r.Header.Get("X-Amz-Object-Lock-Event-Hold-Duration-Years") != wantYears {
							t.Error("native event duration changed")
						}
						if expected != nil && expected.RetainUntilDate == nil && r.Header.Get("X-Amz-Object-Lock-Retain-Until-Date") != "" {
							t.Error("computed date dispatched as intent")
						}
						_, _ = io.Copy(io.Discard, r.Body)
						w.Header().Set("ETag", `"proof"`)
						w.Header().Set("X-Amz-Version-Id", "event-version")
						return
					}
					heads++
					protectedHeadHeaders(w, snapshot, ReservedUploadReceiptMetadataKey, receipt, "event-version", 3)
					switch bad {
					case "missing-event":
						w.Header().Del("X-Amz-Object-Lock-Event-Hold")
					case "duplicate-event":
						w.Header().Add("X-Amz-Object-Lock-Event-Hold", snapshot.MinimumRetention().EventHold)
					case "wrong-status":
						w.Header().Set("X-Amz-Object-Lock-Event-Hold", "unknown")
					case "wrong-duration":
						w.Header().Del("X-Amz-Object-Lock-Event-Hold-Duration-Years")
						w.Header().Set("X-Amz-Object-Lock-Event-Hold-Duration-Days", "1")
					case "both-durations":
						w.Header().Set("X-Amz-Object-Lock-Event-Hold-Duration-Days", "30")
						w.Header().Set("X-Amz-Object-Lock-Event-Hold-Duration-Years", "1")
					case "missing-date":
						w.Header().Del("X-Amz-Object-Lock-Retain-Until-Date")
					case "short-date":
						w.Header().Set("X-Amz-Object-Lock-Retain-Until-Date", snapshot.MinimumRetention().RetainUntilDate.Add(-time.Second).Format(time.RFC3339Nano))
					case "unknown-header":
						w.Header().Set("X-Amz-Object-Lock-Extra", "ON")
					}
				}))
				ctx, err := WithObjectWriteProtection(t.Context(), provider, snapshot, nil)
				if err != nil {
					t.Fatal(err)
				}
				result, err := provider.(TrackedObjectWriter).WriteTrackedObject(ctx, "bucket", "key", receipt, strings.NewReader("abc"), 3, ObjectMetadata{})
				if puts != 1 || heads != 1 || bad == "" && (err != nil || result.VerifiedProtection != snapshot.Proof()) || bad != "" && (!errors.Is(err, ErrUnavailable) || errors.Is(err, ErrWriteRejected)) {
					t.Fatal(result, err, puts, heads)
				}
			})
		}
	}
}

// adr: 599
func TestS3ProtectedEventWritePresign(t *testing.T) {
	for _, policy := range []string{"days", "years", "default", "off-fixed"} {
		t.Run(policy, func(t *testing.T) {
			snapshot := testWriteProtection()
			days, years := int32(30), int32(1)
			wantDays, wantYears := "", ""
			switch policy {
			case "days":
				snapshot.Requested.Retention = &api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &days}}
				wantDays = "30"
			case "years":
				snapshot.Requested.Retention = &api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Years: &years}}
				wantYears = "1"
			case "default":
				snapshot.Requested.Retention = nil
				snapshot.DefaultRetention = &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", DefaultEventHold: &api.ObjectRetentionPeriod{Days: &days}}
			case "off-fixed":
				snapshot.Requested.Retention.EventHold = "OFF"
			}
			provider := protectionTestProvider(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("presign sent a native request") })).(*S3)
			ctx, err := WithObjectWriteProtection(t.Context(), provider, snapshot, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx = WithObjectWriteChecksum(ctx, "kAFQmDzST7DWlj99KOF/cg==")
			size := int64(3)
			result, err := provider.PresignTrackedPut(ctx, "bucket", SignRequest{Method: "PUT", Key: "key", SizeBytes: &size, ExpiresIn: 60}, ObjectWriteConditions{}, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			headers := http.Header{}
			for name, value := range result.Headers {
				headers.Set(name, value)
			}
			u, err := url.Parse(result.URL)
			if err != nil {
				t.Fatal(err)
			}
			if headers.Get("X-Amz-Object-Lock-Event-Hold-Duration-Days") != wantDays || headers.Get("X-Amz-Object-Lock-Event-Hold-Duration-Years") != wantYears || !validProtectedSignedPut(ctx, headers, u.Query().Get("X-Amz-SignedHeaders")) {
				t.Fatal("signed native policy lost", result.Headers)
			}
			if policy != "off-fixed" && headers.Get("X-Amz-Object-Lock-Retain-Until-Date") != "" {
				t.Fatal("admission bound dispatched as intent")
			}
		})
	}
}
