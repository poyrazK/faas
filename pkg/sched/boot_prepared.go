package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// bootPrepared carries reads a wake already made to the boot of the instance
// it admitted, so createRuntimeWithStandards does not make them again on
// the way to vmmd (ADR-1741): the app's rehearsal network, read while the
// admission ran, and the standards capture the admission check read.
type bootPrepared struct {
	instanceID string

	rehearsalRunID string
	haveRehearsal  bool

	capture     state.InstanceApplicationStandardAdmission
	haveCapture bool
}

type bootPreparedKey struct{}

func withBootPrepared(ctx context.Context, p bootPrepared) context.Context {
	return context.WithValue(ctx, bootPreparedKey{}, p)
}

func bootPreparedFor(ctx context.Context, instanceID string) (bootPrepared, bool) {
	p, ok := ctx.Value(bootPreparedKey{}).(bootPrepared)
	return p, ok && p.instanceID == instanceID
}

// bootPrefetch reads, while a wake's admission runs, what its boot needs
// from the app and deployment alone: the private network projection, the
// runtime base key and the rehearsal network. None depends on the instance,
// so the boot no longer waits for them one after another.
type bootPrefetch struct {
	done chan struct{}

	privateNetwork privateNetworkProjection
	baseKey        string
	baseKeyErr     error
	rehearsalRunID string
	rehearsalErr   error
}

func (e *Engine) startBootPrefetch(ctx context.Context, app state.App, dep state.Deployment) *bootPrefetch {
	p := &bootPrefetch{done: make(chan struct{})}
	go func() {
		defer close(p.done)
		concurrently(
			func() { p.privateNetwork = e.privateNetworkProjection(ctx, app) },
			func() { p.baseKey, p.baseKeyErr = e.deploymentBaseKey(ctx, app, dep, layerKey(dep.RootfsKey, dep.ID)) },
			func() { p.rehearsalRunID, p.rehearsalErr = e.rehearsalNetworkRun(ctx, app.ID) },
		)
	}()
	return p
}

func (p *bootPrefetch) wait() *bootPrefetch {
	<-p.done
	return p
}
