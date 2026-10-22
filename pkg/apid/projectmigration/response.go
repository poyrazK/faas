package projectmigration

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// Response captures the existing apply owner's response in memory. It is never
// persisted; migration progress comes from the pre-enqueue journal instead.
type Response struct {
	HeaderMap http.Header
	Code      int
	body      bytes.Buffer
}

func (r *Response) Header() http.Header {
	if r.HeaderMap == nil {
		r.HeaderMap = http.Header{}
	}
	return r.HeaderMap
}
func (r *Response) WriteHeader(code int) {
	if r.Code == 0 {
		r.Code = code
	}
}
func (r *Response) Write(p []byte) (int, error) {
	if r.Code == 0 {
		r.Code = http.StatusOK
	}
	return r.body.Write(p)
}
func (r *Response) Builds() []api.AppliedBuild {
	var out api.ApplyResponse
	_ = json.Unmarshal(r.body.Bytes(), &out)
	return out.Builds
}
