package imaged

import (
	"context"
	"net"
	"strings"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/runtimescan"
	"github.com/onebox-faas/faas/pkg/scanview"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type runtimeScanClientServer struct {
	vmmdpb.UnimplementedVmmdServer
	mode string
}

func (s *runtimeScanClientServer) MaterializeRuntimeScan(ctx context.Context, in *vmmdpb.MaterializeRuntimeScanRequest) (*vmmdpb.MaterializeRuntimeScanResponse, error) {
	r, err := runtimescan.RequestFromProto(in)
	if err != nil {
		return nil, err
	}
	hash, _ := runtimeadmission.HashArtifactSources(r.Sources)
	tree := scanview.Tree{Version: 1, Digest: strings.Repeat("a", 64), ProjectionDigest: strings.Repeat("b", 64), Entries: 2, Bytes: 4}
	out := runtimescan.Receipt{Version: 1, InputHash: r.InputHash, SourcesHash: hash, TargetDir: r.TargetDir, Views: []runtimescan.View{{SourceTree: tree, ProjectionTree: tree}}}.ToProto()
	switch s.mode {
	case "unknown":
		out.ProtoReflect().SetUnknown([]byte{0x30, 1})
	case "nested unknown":
		out.Views[0].SourceTree.ProtoReflect().SetUnknown([]byte{0x30, 1})
	case "mismatch":
		out.SourcesHash = strings.Repeat("f", 64)
	case "missing view":
		out.Views = nil
	case "legacy":
		out.Version = 0
	}
	return out, nil
}

func TestRuntimeScanClientChecksReceiptOverGRPC(t *testing.T) {
	for _, mode := range []string{"complete", "unknown", "nested unknown", "mismatch", "missing view", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			listener := bufconn.Listen(1 << 20)
			server := grpc.NewServer()
			vmmdpb.RegisterVmmdServer(server, &runtimeScanClientServer{mode: mode})
			t.Cleanup(func() { server.Stop(); _ = listener.Close() })
			go func() { _ = server.Serve(listener) }()
			conn, err := grpc.NewClient("passthrough:///runtime-scan", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			client := &VMMClient{conn: conn, cli: vmmdpb.NewVmmdClient(conn)}
			req := runtimescan.Request{Version: 1, InputHash: strings.Repeat("a", 64), TargetDir: t.TempDir(), Sources: []runtimeadmission.ArtifactSource{
				{Kind: "base-image", StorageKey: "base/test.ext4", Digest: "sha256:" + strings.Repeat("1", 64), Bytes: 4},
				{Kind: "app-layer", StorageKey: "apps/test.ext4", Digest: "sha256:" + strings.Repeat("2", 64), Bytes: 4},
			}}
			_, err = client.MaterializeRuntimeScan(t.Context(), req)
			if (err == nil) != (mode == "complete") {
				t.Fatal("unsupported receipt passed native client", mode, err)
			}
		})
	}
}
