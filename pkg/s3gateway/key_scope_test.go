// adr: 20261106114500 — a key-scoped binding reaches only object keys under
// its prefix; everything else, including bucket-wide settings, is refused.
package s3gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestKeyScopeAllowsOnlyRequestsInsideThePrefix(t *testing.T) {
	bucket := state.ObjectBucket{Name: "segments"}
	scoped := state.ObjectS3Credential{Permission: state.ObjectBucketPermissionReadWrite, ManagedAppID: "app", KeyPrefix: "v1/L0/"}
	request := func(method, target string, header map[string]string) *http.Request {
		r := httptest.NewRequest(method, target, nil)
		for k, v := range header {
			r.Header.Set(k, v)
		}
		return r
	}
	for name, tc := range map[string]struct {
		r    *http.Request
		want bool
	}{
		"get inside":                 {request(http.MethodGet, "/segments/v1/L0/a.gz", nil), true},
		"put inside":                 {request(http.MethodPut, "/segments/v1/L0/a.gz", nil), true},
		"delete inside":              {request(http.MethodDelete, "/segments/v1/L0/a.gz", nil), true},
		"multipart part inside":      {request(http.MethodPut, "/segments/v1/L0/big?partNumber=1&uploadId=u", nil), true},
		"get outside":                {request(http.MethodGet, "/segments/v1/L1/a.gz", nil), false},
		"prefix without its slash":   {request(http.MethodGet, "/segments/v1/L0", nil), false},
		"sibling sharing the start":  {request(http.MethodGet, "/segments/v1/L0x/a", nil), false},
		"copy from inside":           {request(http.MethodPut, "/segments/v1/L0/b", map[string]string{"x-amz-copy-source": "/segments/v1/L0/a"}), true},
		"copy from outside":          {request(http.MethodPut, "/segments/v1/L0/b", map[string]string{"x-amz-copy-source": "/segments/v1/L1/a"}), false},
		"copy from another bucket":   {request(http.MethodPut, "/segments/v1/L0/b", map[string]string{"x-amz-copy-source": "/other/v1/L0/a"}), false},
		"copy source escaped":        {request(http.MethodPut, "/segments/v1/L0/b", map[string]string{"x-amz-copy-source": "/segments/v1%2FL1%2Fa"}), false},
		"list v2 inside":             {request(http.MethodGet, "/segments?list-type=2&prefix=v1/L0/day-1/", nil), true},
		"list v1 inside":             {request(http.MethodGet, "/segments?prefix=v1/L0/&marker=v1/L0/a", nil), true},
		"list without prefix":        {request(http.MethodGet, "/segments?list-type=2", nil), false},
		"list outside":               {request(http.MethodGet, "/segments?list-type=2&prefix=v1/", nil), false},
		"list two prefixes":          {request(http.MethodGet, "/segments?list-type=2&prefix=v1/L0/&prefix=v1/", nil), false},
		"versions inside":            {request(http.MethodGet, "/segments?versions&prefix=v1/L0/", nil), true},
		"uploads inside":             {request(http.MethodGet, "/segments?uploads&prefix=v1/L0/", nil), true},
		"uploads without prefix":     {request(http.MethodGet, "/segments?uploads", nil), false},
		"bucket head":                {request(http.MethodHead, "/segments", nil), true},
		"bucket location":            {request(http.MethodGet, "/segments?location", nil), true},
		"bulk delete (keys checked)": {request(http.MethodPost, "/segments?delete", nil), true},
		"lifecycle":                  {request(http.MethodGet, "/segments?lifecycle", nil), false},
		"versioning":                 {request(http.MethodGet, "/segments?versioning", nil), false},
		"notifications":              {request(http.MethodGet, "/segments?notification", nil), false},
		"list buckets":               {request(http.MethodGet, "/", nil), false},
		"another bucket":             {request(http.MethodGet, "/other/v1/L0/a", nil), false},
	} {
		if got := keyScopeAllows(tc.r, scoped, bucket); got != tc.want {
			t.Errorf("%s: keyScopeAllows = %t, want %t", name, got, tc.want)
		}
	}
	unscoped := scoped
	unscoped.KeyPrefix = ""
	if !keyScopeAllows(request(http.MethodGet, "/segments?lifecycle", nil), unscoped, bucket) {
		t.Fatal("an unscoped credential was limited")
	}
}

func TestKeyScopedBulkDeleteRefusesAnyKeyOutside(t *testing.T) {
	scoped := state.ObjectS3Credential{KeyPrefix: "v1/L0/"}
	inside := deleteObjectsRequest{Objects: []deleteObjectTarget{{Key: "v1/L0/a"}, {Key: "v1/L0/b"}}}
	if !deleteTargetsInScope(scoped, inside) {
		t.Fatal("a delete inside the prefix was refused")
	}
	mixed := deleteObjectsRequest{Objects: []deleteObjectTarget{{Key: "v1/L0/a"}, {Key: "v1/L1/b"}}}
	if deleteTargetsInScope(scoped, mixed) {
		t.Fatal("a delete with a key outside the prefix was admitted")
	}
	if !deleteTargetsInScope(state.ObjectS3Credential{}, mixed) {
		t.Fatal("an unscoped credential was limited")
	}
}

// Bucket-wide settings stay with customer-created read_write keys: neither a
// compute binding's previous key during rotation nor a key-scoped key holds
// them.
func TestBucketSettingsExcludeRotationStagesAndScopedKeys(t *testing.T) {
	h := &Handler{}
	customer := state.ObjectS3Credential{Permission: state.ObjectBucketPermissionReadWrite}
	if !h.can(customer, bucketSettingsPermission) {
		t.Fatal("a customer read_write key lost bucket settings")
	}
	stage := customer
	stage.RotationParentID = "binding"
	if h.can(stage, bucketSettingsPermission) {
		t.Fatal("a rotation stage holds bucket settings")
	}
	scoped := customer
	scoped.KeyPrefix = "v1/"
	if h.can(scoped, bucketSettingsPermission) {
		t.Fatal("a key-scoped key holds bucket settings")
	}
	if !h.can(stage, state.ObjectBucketPermissionWrite) {
		t.Fatal("a rotation stage lost object writes")
	}
}
