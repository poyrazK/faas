package wire

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestPrivateForwardRequiresExplicitAuthorizedContext(t *testing.T) {
	for _, private := range []bool{false, true} {
		ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(privateForwardKey, "1", "other", "preserved"))
		if private {
			ctx = WithPrivateForward(ctx)
		}
		out := ForwardContext(ctx)
		md, _ := metadata.FromOutgoingContext(out)
		if md.Get("other")[0] != "preserved" {
			t.Fatal("unrelated transport metadata lost")
		}
		if got := IsPrivateForward(metadata.NewIncomingContext(context.Background(), md)); got != private {
			t.Fatalf("private=%t trusted inherited metadata: %v", private, md)
		}
	}
}

func TestPrivateForwardUnknownAndDuplicateMarkersArePublic(t *testing.T) {
	for _, values := range [][]string{nil, {""}, {"true"}, {"0"}, {"1", "1"}, {"1", "0"}} {
		ctx := metadata.NewIncomingContext(t.Context(), metadata.MD{privateForwardKey: values})
		if IsPrivateForward(ctx) {
			t.Fatalf("ambiguous marker selected unmetered route: %v", values)
		}
	}
}
