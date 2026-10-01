package main

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *server) proxyDevBridge(w http.ResponseWriter, r *http.Request) {
	target, err := url.Parse(s.devBridgeURL)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("invalid development bridge relay configuration"))
		return
	}
	ip := net.ParseIP(target.Hostname())
	if !s.devBridgeEnabled || err != nil || target.Scheme != "http" || ip == nil || !ip.IsLoopback() {
		api.WriteProblem(w, api.NewProblem(503, "dev_bridge_unavailable", "Dev Bridge unavailable", "the relay preview is not configured"))
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = s.observeDevBridgeProxy(http.DefaultTransport, r)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		api.WriteProblem(w, api.NewProblem(503, "dev_bridge_disconnected", "Dev Bridge unavailable", "the relay is disconnected"))
	}
	proxy.FlushInterval = -1
	_ = http.NewResponseController(w).EnableFullDuplex()
	proxy.ServeHTTP(w, r)
}
