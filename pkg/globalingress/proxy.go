package globalingress

// adr: 784
import (
	"context"
	"errors"
	"net/http"
	"net/http/httputil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

const HopHeader = "X-Gregale-Global-Ingress-Hop"

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	app, global := g.appHost(r.Host)
	if !global {
		g.next.ServeHTTP(w, r)
		return
	}
	if app == "" {
		http.NotFound(w, r)
		return
	}
	if len(r.Header.Values(HopHeader)) != 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusLoopDetected, api.CodeGlobalIngressLoop, "Routing loop", "the request already traversed global ingress"))
		return
	}
	target, found, err := g.target(app)
	if err != nil {
		ingressUnavailable(w)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	g.proxy(app, target).ServeHTTP(w, r)
}

func ingressUnavailable(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeGlobalIngressUnavailable, "Regional serving unavailable", "no current healthy eligible regional route is available"))
}

func (g *Gateway) proxy(app string, target runtimequota.IngressTarget) *httputil.ReverseProxy {
	origin := parseOrigin(target.Origin)
	return &httputil.ReverseProxy{
		Transport:     g.transport,
		FlushInterval: -1,
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(origin)
			api.ClearGuestIdentityHeaders(req.Out.Header)
			req.Out.Header.Del(api.InvocationSourceHeader)
			// Forward ordinary app credentials only; no directory credential,
			// grant or operational claim is added by this transport.
			req.Out.Header.Del("Forwarded")
			req.Out.Header.Del("X-Forwarded-For")
			req.Out.Header.Del("X-Forwarded-Host")
			req.Out.Header.Del("X-Forwarded-Proto")
			req.SetXForwarded()
			req.Out.Header.Set(HopHeader, "1")
			req.Out.Header.Set(api.RequestIDHeader, req.In.Header.Get(api.RequestIDHeader))
		},
		ModifyResponse: func(response *http.Response) error {
			httpsec.StripStaticHeaders(response.Header)
			response.Header.Del(api.RequestIDHeader)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() == nil && !errors.Is(err, context.Canceled) {
				g.failed(app, target)
			}
			// Never replay an app request to another region: the first target
			// may have performed a side effect before its response was lost.
			ingressUnavailable(w)
		},
	}
}
