package bindingprobe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
)

// A cold verification VM's first request can die before any response; the
// read-only listing is tried once more instead of reporting a working
// binding as unreachable.
func TestObjectStorageRetriesListingThatGotNoResponse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>private-bucket</Name></ListBucketResult>`)
	})}
	go func() { _ = srv.Serve(&dropFirstListener{Listener: ln}) }()
	defer func() { _ = srv.Close() }()
	env := objectStorageTestEnv("http://" + ln.Addr().String())
	report := ObjectStorage(context.Background(), "ASSETS", func(key string) string { return env[key] })
	if !report.Passed() || requests != 1 {
		t.Fatalf("report=%+v requests=%d", report, requests)
	}
}

// dropFirstListener closes its first accepted connection unanswered.
type dropFirstListener struct {
	net.Listener
	dropped bool
}

func (l *dropFirstListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil && !l.dropped {
		l.dropped = true
		_ = conn.Close()
		return l.Listener.Accept()
	}
	return conn, err
}

func TestObjectStorageConnectionDetailNamesTheFailedStep(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"dns":     {&net.DNSError{Err: "no such host", Name: "s3.example"}, "DNS lookup"},
		"dial":    {&net.OpError{Op: "dial", Err: errors.New("connection refused")}, "could not open a connection"},
		"timeout": {context.DeadlineExceeded, "did not answer within"},
		"other":   {errors.New("boom"), "could not reach the S3 endpoint"},
	} {
		got := objectStorageConnectionDetail(tc.err)
		if !strings.Contains(got, tc.want) || strings.Contains(got, "s3.example") || strings.Contains(got, "boom") {
			t.Errorf("%s: %q", name, got)
		}
	}
}
