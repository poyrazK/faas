package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/metadata"
)

func TestPrivateServiceGuestRequestMarksAuthorizedRouteWithoutSigningKey(t *testing.T) {
	proxy := NewServiceProxy(ServiceProxyConfig{})
	request := httptest.NewRequest(http.MethodGet, "http://inventory.internal/items", nil)
	request.Header.Set("Gregale-Private-Forward", "1")
	guest := request.Clone(request.Context())
	ctx := proxy.prepareGuestRequest(guest.Context(), guest, "/items", ServiceTarget{AppID: "inventory"}, ServiceCaller{AppID: "worker"})
	md, _ := metadata.FromOutgoingContext(wire.ForwardContext(ctx))
	if !wire.IsPrivateForward(metadata.NewIncomingContext(t.Context(), md)) {
		t.Fatal("authorized private route depends on optional assertion signer")
	}
	public, _ := metadata.FromOutgoingContext(wire.ForwardContext(request.Context()))
	if wire.IsPrivateForward(metadata.NewIncomingContext(t.Context(), public)) {
		t.Fatal("guest HTTP header selected the private route")
	}
}
