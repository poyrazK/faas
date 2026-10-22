package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	objruntime "github.com/onebox-faas/faas/pkg/rehearsalobj/runtime"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func startRehearsalObjectProxy(ctx context.Context, deps runDeps, serviceAddress, node string, errc chan<- error) error {
	if osGetenv("FAAS_REHEARSAL_OBJECT_FIXTURES") != "1" {
		return nil
	}
	bridge, e := serviceProxyBridgeIP(serviceAddress)
	if e != nil || deps.pgStore == nil {
		return errors.New("rehearsal objects require the private bridge and durable state")
	}
	identities, e := secretbox.LoadFleetAndHostKeys(deps.hostKeyDir)
	if e != nil || len(identities) == 0 {
		return errors.New("rehearsal object credential opener unavailable")
	}
	callers := newServiceProxyCallerIdentityResolver(deps.pgStore.ListAllInstances, node)
	callers.lookup = deps.pgStore.LiveServiceCallersByHostIP
	collector := objruntime.NewCollector(deps.pgStore)
	handler := &objruntime.Handler{Catalog: deps.pgStore, Collector: collector, Resolve: func(ctx context.Context, remote string) (string, string, error) {
		return callers.ResolveIdentity(ctx, remote)
	}, OpenSecret: func(sealed []byte) (string, error) {
		namespace, plaintext, e := secretbox.OpenBytesMulti(identities, sealed)
		if e != nil || namespace != rehearsalobj.CredentialNamespace {
			return "", errors.New("invalid rehearsal object credential")
		}
		return string(plaintext), nil
	}}
	l, e := deps.listen("tcp", net.JoinHostPort(bridge.String(), strconv.Itoa(rehearsalobj.ProxyPort)))
	if e != nil {
		return e
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	go collector.Run(ctx)
	go func() { <-ctx.Done(); _ = server.Close() }()
	go func() {
		if e := server.Serve(l); e != nil && !errors.Is(e, http.ErrServerClosed) {
			collector.Fail()
			select {
			case errc <- errors.New("private rehearsal object listener stopped"):
			case <-ctx.Done():
			}
		}
	}()
	return nil
}
