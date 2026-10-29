package s3gateway

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// errUploadTooSlow ends a request body that stopped making progress. Upload
// slots are scarce (MaxConcurrentPuts per gateway, half per account), and the
// overall transfer budget is 30 minutes, so without a progress bound a client
// trickling a byte at a time held its slot for the whole budget.
var errUploadTooSlow = errors.New("s3 gateway: request body too slow")

// progressBody holds a request body to api.ObjectUploadIdleTimeout between
// bytes and, after api.ObjectUploadMinRateGrace, to an average of
// api.ObjectUploadMinBytesPerSecond. Each read moves the connection's read
// deadline to the sooner of the idle bound and the transfer deadline.
type progressBody struct {
	body       io.ReadCloser
	controller *http.ResponseController
	deadline   time.Time
	started    time.Time
	read       int64
	now        func() time.Time
}

func newProgressBody(w http.ResponseWriter, body io.ReadCloser, deadline time.Time) *progressBody {
	return &progressBody{body: body, controller: http.NewResponseController(w), deadline: deadline, started: time.Now(), now: time.Now}
}

func (p *progressBody) Read(b []byte) (int, error) {
	now := p.now()
	if elapsed := now.Sub(p.started); elapsed > api.ObjectUploadMinRateGrace &&
		float64(p.read) < elapsed.Seconds()*api.ObjectUploadMinBytesPerSecond {
		return 0, errUploadTooSlow
	}
	next := now.Add(api.ObjectUploadIdleTimeout)
	if !p.deadline.IsZero() && p.deadline.Before(next) {
		next = p.deadline
	}
	_ = p.controller.SetReadDeadline(next)
	n, err := p.body.Read(b)
	p.read += int64(n)
	return n, err
}

func (p *progressBody) Close() error { return p.body.Close() }
