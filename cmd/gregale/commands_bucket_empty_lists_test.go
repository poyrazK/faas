package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 33 H33-23 and H33-48: `bucket version list` and `bucket writes list`
// printed nothing for an empty bucket.
func TestBucketEmptyListsSaySo(t *testing.T) {
	bucket := uuid.NewString()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"bucket", "versions", "list", "demo", bucket}, "No object versions in bucket " + bucket + "."},
		{[]string{"bucket", "writes", "list", "demo", bucket}, "No pending write receipts for bucket " + bucket + "."},
	} {
		var out bytes.Buffer
		old := *main.OsStdout
		*main.OsStdout = &out
		code := main.Run(tc.args)
		*main.OsStdout = old
		if code != 0 || !strings.Contains(out.String(), tc.want) {
			t.Errorf("%v: exit %d, output %q", tc.args, code, out.String())
		}
	}
}
