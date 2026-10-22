package runtimescan

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/scanview"
)

func scanContractFixture(t *testing.T) (Request, Receipt) {
	t.Helper()
	r := Request{Version: Version, InputHash: strings.Repeat("a", 64), TargetDir: t.TempDir(), Sources: []runtimeadmission.ArtifactSource{
		{Kind: "base-image", StorageKey: "base/fixture.ext4", Digest: imagechain.Digest([]byte("base")), Bytes: 4},
		{Kind: "app-layer", StorageKey: "apps/main.ext4", Digest: imagechain.Digest([]byte("main")), Bytes: 4},
		{Kind: "sidecar-layer", WorkloadName: "metrics", StorageKey: "sidecars/metrics.ext4", Digest: imagechain.Digest([]byte("sidecar")), Bytes: 7},
	}}
	hash, err := runtimeadmission.HashArtifactSources(r.Sources)
	if err != nil {
		t.Fatal(err)
	}
	tree := scanview.Tree{Version: scanview.Version, Digest: strings.Repeat("b", 64), ProjectionDigest: strings.Repeat("c", 64), Entries: 2, Bytes: 7}
	return r, Receipt{Version: Version, InputHash: r.InputHash, SourcesHash: hash, TargetDir: r.TargetDir, Views: []View{{SourceTree: tree, ProjectionTree: tree}, {WorkloadName: "metrics", SourceTree: tree, ProjectionTree: tree}}}
}

func TestRuntimeScanReceiptBindsCompleteSourceSetAndEveryView(t *testing.T) {
	for _, mode := range []string{"complete", "order", "version", "input", "sources", "target", "missing sidecar", "duplicate main", "unknown workload", "raw tree", "projection bytes", "projection links", "aggregate bytes", "aggregate entries"} {
		t.Run(mode, func(t *testing.T) {
			request, receipt := scanContractFixture(t)
			switch mode {
			case "order":
				slices.Reverse(request.Sources)
				slices.Reverse(receipt.Views)
			case "version":
				receipt.Version++
			case "input":
				receipt.InputHash = strings.Repeat("d", 64)
			case "sources":
				request.Sources[1].Bytes++
			case "target":
				receipt.TargetDir += "-other"
			case "missing sidecar":
				receipt.Views = receipt.Views[:1]
			case "duplicate main":
				receipt.Views[1].WorkloadName = ""
			case "unknown workload":
				receipt.Views[1].WorkloadName = "unknown"
			case "raw tree":
				receipt.Views[0].SourceTree.Digest = ""
			case "projection bytes":
				receipt.Views[0].ProjectionTree.Bytes++
			case "projection links":
				receipt.Views[0].ProjectionTree.ProjectionDigest = strings.Repeat("d", 64)
			case "aggregate bytes":
				for i := range receipt.Views {
					receipt.Views[i].SourceTree.Bytes = api.ApplicationStandardRuntimeScanMaxBytes
					receipt.Views[i].ProjectionTree.Bytes = api.ApplicationStandardRuntimeScanMaxBytes
				}
			case "aggregate entries":
				for i := range receipt.Views {
					receipt.Views[i].SourceTree.Entries = api.ApplicationStandardRuntimeScanMaxEntries
					receipt.Views[i].ProjectionTree.Entries = api.ApplicationStandardRuntimeScanMaxEntries
				}
			}
			if valid := mode == "complete" || mode == "order"; (receipt.Check(request) == nil) != valid {
				t.Fatal("accepted mismatched or incomplete runtime view", mode)
			}
		})
	}
}

func TestRuntimeScanWireRejectsUnknownOrUnboundedReceipts(t *testing.T) {
	for _, mode := range []string{"complete", "request unknown", "source unknown", "response unknown", "view unknown", "tree unknown", "overflow", "missing tree", "future tree"} {
		t.Run(mode, func(t *testing.T) {
			request, receipt := scanContractFixture(t)
			in, out := request.ToProto(), receipt.ToProto()
			switch mode {
			case "request unknown":
				in.ProtoReflect().SetUnknown([]byte{0x28, 1})
			case "source unknown":
				in.Sources[0].ProtoReflect().SetUnknown([]byte{0x30, 1})
			case "response unknown":
				out.ProtoReflect().SetUnknown([]byte{0x30, 1})
			case "view unknown":
				out.Views[0].ProtoReflect().SetUnknown([]byte{0x20, 1})
			case "tree unknown":
				out.Views[0].SourceTree.ProtoReflect().SetUnknown([]byte{0x30, 1})
			case "overflow":
				out.Views[0].SourceTree.Entries = 1 << 62
			case "missing tree":
				out.Views[0].SourceTree = nil
			case "future tree":
				out.Views[0].ProjectionTree.Version++
			}
			decoded, reqErr := RequestFromProto(in)
			_, receiptErr := ReceiptFromProto(out, decoded)
			if (reqErr == nil && receiptErr == nil) != (mode == "complete") {
				t.Fatal("unsupported wire acquired a receipt", mode, reqErr, receiptErr)
			}
		})
	}
}

func TestRuntimeScanRequiresExplicitBootBaseAndBoundedSources(t *testing.T) {
	for _, mode := range []string{"base missing", "base duplicate", "main missing", "unknown layout", "artifact aggregate", "relative target", "future request"} {
		t.Run(mode, func(t *testing.T) {
			request, _ := scanContractFixture(t)
			switch mode {
			case "base missing":
				request.Sources = request.Sources[1:]
				request.Sources[0].Kind = "full-rootfs"
			case "base duplicate":
				request.Sources[2] = request.Sources[0]
			case "main missing":
				request.Sources = request.Sources[:1]
			case "unknown layout":
				request.Sources[1].Kind = "flattened"
			case "artifact aggregate":
				for i := range request.Sources {
					request.Sources[i].Bytes = api.ApplicationStandardBaseMaxArtifactBytes
				}
			case "relative target":
				request.TargetDir = "relative"
			case "future request":
				request.Version++
			}
			if err := request.Validate(); err == nil {
				t.Fatal("invalid input acquired native scanner capability", mode)
			}
		})
	}
}
