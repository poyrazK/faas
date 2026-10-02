package imaged

// adr: 431

import (
	"context"
	"net"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type parentWireServer struct {
	vmmdpb.UnimplementedVmmdServer
	mode string
}

func (s *parentWireServer) MaterializeVerifiedParentExt4(_ context.Context, r *vmmdpb.MaterializeVerifiedParentExt4Request) (*vmmdpb.MaterializeVerifiedParentExt4Response, error) {
	out := &vmmdpb.MaterializeVerifiedParentExt4Response{StorageKey: r.StorageKey, TargetDir: r.TargetDir, ArtifactDigest: r.ArtifactDigest, ArtifactBytes: r.ArtifactBytes}
	if s.mode == "mismatch" {
		out.ArtifactBytes++
	}
	if s.mode == "unknown" {
		out.ProtoReflect().SetUnknown([]byte{0x28, 1})
	}
	return out, nil
}
func TestVerifiedParentClientRequiresCompleteReceipt(t *testing.T) {
	for _, mode := range []string{"complete", "mismatch", "unknown", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			lis := bufconn.Listen(1 << 20)
			defer lis.Close()
			server := grpc.NewServer()
			defer server.Stop()
			if mode == "legacy" {
				vmmdpb.RegisterVmmdServer(server, &vmmdpb.UnimplementedVmmdServer{})
			} else {
				vmmdpb.RegisterVmmdServer(server, &parentWireServer{mode: mode})
			}
			go func() { _ = server.Serve(lis) }()
			conn, err := grpc.NewClient("passthrough:///parent", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			client := &VMMClient{conn: conn, cli: vmmdpb.NewVmmdClient(conn)}
			expected := imagechain.ParentMaterialization{Artifact: imagechain.BaseArtifact{StorageKey: "base/parent.ext4", Digest: imagechain.Digest([]byte("source")), Bytes: 6}, TargetDir: "/dev/shm/faas-base-staging/test"}
			actual, err := client.MaterializeVerifiedParentExt4(t.Context(), expected)
			if mode == "complete" {
				if err != nil || actual != expected {
					t.Fatalf("wire identity lost: %v", err)
				}
				return
			}
			if err == nil || actual.Valid() {
				t.Fatal("unsupported server minted a receipt")
			}
		})
	}
}
