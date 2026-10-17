package objectqualification

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"golang.org/x/time/rate"
)

type faultProxy struct {
	server          *httptest.Server
	target          atomic.Value
	partition       atomic.Bool
	dropResponse    atomic.Bool
	dropBefore      atomic.Bool
	puts            atomic.Int64
	deletes         atomic.Int64
	lastReceipt     atomic.Value
	settlementProbe atomic.Pointer[settlementObservation]
	transport       *http.Transport
	proxy           *httputil.ReverseProxy
	delay           time.Duration
	bandwidth       *rate.Limiter
}

func newFaultProxy() *faultProxy {
	p := &faultProxy{transport: http.DefaultTransport.(*http.Transport).Clone()}
	p.target.Store("")
	p.lastReceipt.Store("")
	p.proxy = &httputil.ReverseProxy{Rewrite: p.rewrite, Transport: p.transport, ModifyResponse: p.modify, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "qualification upstream unavailable", http.StatusBadGateway)
	}}
	p.server = httptest.NewServer(http.HandlerFunc(p.serveHTTP))
	return p
}

func (p *faultProxy) rewrite(r *httputil.ProxyRequest) {
	target, _ := url.Parse(p.target.Load().(string))
	r.SetURL(target)
	r.Out.Host = r.In.Host
	r.Out.URL.RawQuery = r.In.URL.RawQuery
	// Preserve the signed Host while changing the transport destination.
}

func (p *faultProxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete && !r.URL.Query().Has(objectnode.ReplicationQuery) {
		p.deletes.Add(1)
	}
	if r.URL.Query().Has(objectnode.ReplicationQuery) && p.partition.Load() {
		http.Error(w, "qualification replication partition", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Query().Has(objectnode.ReplicationQuery) && p.delay > 0 {
		timer := time.NewTimer(p.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
	}
	if r.Method == http.MethodPut && !r.URL.Query().Has(objectnode.ReplicationQuery) {
		p.puts.Add(1)
		receipt := r.Header.Get(objectnode.ReceiptMetadataHeader)
		if receipt == "" {
			receipt = r.URL.Query().Get(objectnode.ReceiptMetadataHeader)
		}
		if receipt != "" {
			p.lastReceipt.Store(receipt)
		}
		if p.dropBefore.CompareAndSwap(true, false) {
			closeHTTP(w)
			return
		}
	}
	if p.target.Load().(string) == "" {
		http.Error(w, "qualification node starting", http.StatusServiceUnavailable)
		return
	}
	p.proxy.ServeHTTP(w, r)
}

func closeHTTP(w http.ResponseWriter) {
	connection, _, err := http.NewResponseController(w).Hijack()
	if err == nil {
		_ = connection.Close()
	}
}

var errDroppedResponse = errors.New("qualification intentionally lost the provider acknowledgement")

func (p *faultProxy) modify(response *http.Response) error {
	if err := p.observeSettlement(response); err != nil {
		return err
	}
	if response.Request.URL.Query().Get(objectnode.ReplicationQuery) == "blob" && p.bandwidth != nil {
		response.Body = &shapedBody{ReadCloser: response.Body, ctx: response.Request.Context(), limiter: p.bandwidth}
	}
	if response.Request.Method == http.MethodPut && !response.Request.URL.Query().Has(objectnode.ReplicationQuery) && p.dropResponse.CompareAndSwap(true, false) {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return errDroppedResponse
	}
	return nil
}

func (p *faultProxy) configureNetwork(o Options) {
	p.delay = o.ReplicationDelay
	if o.ReplicationBytesPerSecond > 0 {
		p.bandwidth = rate.NewLimiter(rate.Limit(o.ReplicationBytesPerSecond), api.ObjectQualificationAgentChunkBytes)
	}
}

type shapedBody struct {
	io.ReadCloser
	ctx     context.Context
	limiter *rate.Limiter
}

func (s *shapedBody) Read(buffer []byte) (int, error) {
	buffer = buffer[:min(len(buffer), api.ObjectQualificationAgentChunkBytes)]
	if err := s.limiter.WaitN(s.ctx, len(buffer)); err != nil {
		return 0, err
	}
	return s.ReadCloser.Read(buffer)
}
