package object

import (
	"errors"
	"github.com/google/uuid"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
)

func TestOwnedInventoryAccountingAcrossRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	node, err := objectnode.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = node.CreateBucket("physical"); err != nil {
		t.Fatal(err)
	}
	if _, err = node.Put(t.Context(), "physical", "key", strings.NewReader("original"), 8, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = node.Put(t.Context(), "physical", "key", strings.NewReader("new"), 3, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	u, err := node.CreateMultipart("physical", "unfinished", objectnode.MultipartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = node.UploadPart(t.Context(), "physical", "unfinished", u.ID, 1, strings.NewReader("part"), 4, objectnode.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = node.Close(); err != nil {
		t.Fatal(err)
	}
	node, err = objectnode.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	h, err := s3gateway.NewDiskHandler(s3gateway.DiskConfig{Store: node, AccessKeyID: "node-key", SecretAccessKey: "node-secret", Region: "us-east-1", MinFreeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(h)
	defer upstream.Close()
	st, registry, bucket, _ := receiptRecoveryFixture(t, upstream.URL)
	svc := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
	scan := func(token string) error {
		t.Helper()
		if err := st.ClaimObjectInventory(t.Context(), bucket.ID, token); err != nil {
			t.Fatal(err)
		}
		return svc.ScanObjectInventory(t.Context(), st, bucket, token)
	}
	if err = scan("after-restart"); err != nil {
		t.Fatal(err)
	}
	usage, err := st.ObjectUsage(t.Context(), bucket.AccountID, time.Now())
	if err != nil || len(usage.Buckets) != 1 || usage.Buckets[0].ObservedBytes != 3 || usage.Buckets[0].ObservedKeys != 1 {
		t.Fatal("committed inventory included staging or double counted replacement", usage, err)
	}
	disk, err := node.Usage(t.Context())
	if err != nil || disk.CommittedBytes != 3 || disk.MultipartBytes != 4 {
		t.Fatal(disk, err)
	}
	if err = node.AbortMultipart("physical", "unfinished", u.ID); err != nil {
		t.Fatal(err)
	}
	if err = node.Delete("physical", "key"); err != nil {
		t.Fatal(err)
	}
	job, err := st.RequestObjectCapacityReconciliation(t.Context(), bucket.AccountID, bucket.AppID, bucket.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err = st.ClaimObjectCapacityReconciliation(t.Context(), job.ID, "owned-capacity")
	if err != nil || job.State != "scanning" {
		t.Fatal(job, err)
	}
	job, err = svc.scanObjectCapacity(t.Context(), st, bucket, job)
	if err != nil || job.State != "completed" || job.BeforeBytes != 3 || job.AfterBytes != 0 || job.ReclaimedBytes != 3 || job.ReclaimedKeys != 1 {
		t.Fatal("capacity drift did not reconcile through the existing fence", job, err)
	}
	usage, err = st.ObjectUsage(t.Context(), bucket.AccountID, time.Now())
	if err != nil || usage.Buckets[0].ObservedBytes != 0 || usage.Buckets[0].ObservedKeys != 0 {
		t.Fatal(usage, err)
	}
	if err = st.BeginObjectWrite(t.Context(), bucket.AccountID, bucket.ID, uuid.NewString(), "pending", 9, registry.Accounting); err != nil {
		t.Fatal(err)
	}
	if err = scan("pending-writer"); err != nil {
		t.Fatal(err)
	}
	usage, err = st.ObjectUsage(t.Context(), bucket.AccountID, time.Now())
	if err != nil || usage.Buckets[0].ObservedBytes != 0 || usage.Buckets[0].GrantedBytes != 9 || usage.Buckets[0].GrantedKeys != 1 {
		t.Fatal("inventory released an unsettled writer reservation", usage, err)
	}
	o, err := node.Put(t.Context(), "physical", "damaged", strings.NewReader("kept"), 4, objectnode.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = scan("before-damage"); err != nil {
		t.Fatal(err)
	}
	_, file, err := node.Get("physical", o.Key)
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	_ = file.Close()
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = scan("after-damage"); !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("missing blob accepted as inventory", err)
	}
	usage, err = st.ObjectUsage(t.Context(), bucket.AccountID, time.Now())
	if err != nil || usage.Buckets[0].ObservedBytes != 4 || usage.Buckets[0].ObservedKeys != 1 {
		t.Fatal("failed scan changed previous accounting", usage, err)
	}
}
