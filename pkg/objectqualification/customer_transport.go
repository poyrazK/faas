//go:build linux

package objectqualification

import (
	"io"
	"net/http"
	"sync"
)

// Count actual customer HTTP activity, including requests waiting for a prior
// replica confirmation. A stalled request need not publish a new journal entry.
type customerTransport struct {
	next http.RoundTripper
	lab  *lab
}

func (t customerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.lab.customerRequests.Add(1)
	t.lab.customerActive.Add(1)
	response, err := t.next.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil {
		t.lab.customerActive.Add(-1)
		return response, err
	}
	response.Body = &customerBody{ReadCloser: response.Body, lab: t.lab}
	return response, nil
}

type customerBody struct {
	io.ReadCloser
	lab  *lab
	once sync.Once
}

func (b *customerBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { b.lab.customerActive.Add(-1) })
	return err
}
