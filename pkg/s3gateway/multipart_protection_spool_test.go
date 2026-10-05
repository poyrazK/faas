package s3gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 590
func TestProtectedMultipartPartSpool(t *testing.T) {
	for _, mode := range []string{"valid", "bad digest", "disk full"} {
		t.Run(mode, func(t *testing.T) {
			f := newMultipartCopyIntegrationWithProvider(t, state.NewMemStore(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("staging dispatched provider request") }))
			h := f.handler
			if mode == "disk full" {
				h.spoolAvailable = func() (uint64, error) { return 0, nil }
			}
			r := httptest.NewRequest(http.MethodPut, "http://s3.test/assets/key", strings.NewReader("abc"))
			if mode == "bad digest" {
				r.Header.Set("Content-MD5", "1B2M2Y8AsgTpgAmY7PhCfg==")
			}
			body, err := newRequestIntegrityReader(r.Body, r.ContentLength, "UNSIGNED-PAYLOAD", r.Header)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			file, digest, cleanup, ok := h.stageProtectedMultipartPart(w, r, requestContext{}, body)
			if mode == "valid" {
				if !ok || digest != "kAFQmDzST7DWlj99KOF/cg==" {
					t.Fatal(ok, digest, w.Code)
				}
				data, err := io.ReadAll(file)
				if err != nil || string(data) != "abc" {
					t.Fatal(string(data), err)
				}
				cleanup()
			} else if ok || mode == "bad digest" && w.Code != 400 || mode == "disk full" && w.Code != 503 {
				t.Fatal(ok, w.Code, w.Body.String())
			}
			files, err := os.ReadDir(h.spoolDir)
			if err != nil || len(files) != 0 || h.spoolReserved != 0 {
				t.Fatal("staging leaked disk custody", files, h.spoolReserved, err)
			}
		})
	}
}
