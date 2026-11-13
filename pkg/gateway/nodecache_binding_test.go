// adr: 20261108124106
package gateway

import (
	"context"
	"testing"

	"google.golang.org/grpc"
)

// With a node binding, the cache dials through the bound dialer with the
// node's name, and refuses a node whose name it cannot resolve.
func TestNodeClientCacheBindsTheDialToTheNodeName(t *testing.T) {
	previousResolver := resolveNodeTarget
	t.Cleanup(func() { resolveNodeTarget = previousResolver })
	SetNodeResolver(func(context.Context, string) (string, bool) { return "tcp://192.0.2.1:50051", true })

	names := map[string]string{"node-1": "fsn-2.faas"}
	var dialedName string
	cache := NewNodeClientCache(func(context.Context, string) (*grpc.ClientConn, error) {
		t.Fatal("the unbound dialer ran with a binding set")
		return nil, nil
	}, nil).WithNodeBinding(func(_ context.Context, id string) (string, bool) {
		n, ok := names[id]
		return n, ok
	}, func(_ context.Context, _ string, name string) (*grpc.ClientConn, error) {
		dialedName = name
		return &grpc.ClientConn{}, nil
	})

	_, closer, ok := cache.ClientFor(context.Background(), "node-1")
	if !ok {
		t.Fatal("bound dial failed")
	}
	_ = closer.Close()
	if dialedName != "fsn-2.faas" {
		t.Fatalf("dialed with name %q, want fsn-2.faas", dialedName)
	}
	if _, _, ok := cache.ClientFor(context.Background(), "node-unknown"); ok {
		t.Fatal("a node with no resolvable name was dialed")
	}
}
