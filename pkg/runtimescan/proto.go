package runtimescan

import (
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/scanview"
)

func RequestFromProto(in *vmmdpb.MaterializeRuntimeScanRequest) (Request, error) {
	if in == nil || runtimeadmission.RejectUnknown(in) != nil {
		return Request{}, runtimeadmission.ErrInvalid
	}
	sources, err := runtimeadmission.ArtifactSourcesFromProto(in.Sources)
	if err != nil {
		return Request{}, err
	}
	out := Request{Version: in.Version, InputHash: in.InputHash, Sources: sources, TargetDir: in.TargetDir}
	return out, out.Validate()
}

func (r Request) ToProto() *vmmdpb.MaterializeRuntimeScanRequest {
	out := &vmmdpb.MaterializeRuntimeScanRequest{Version: r.Version, InputHash: r.InputHash, TargetDir: r.TargetDir}
	for _, source := range r.Sources {
		out.Sources = append(out.Sources, source.ToProto())
	}
	return out
}

func ReceiptFromProto(in *vmmdpb.MaterializeRuntimeScanResponse, expected Request) (Receipt, error) {
	if in == nil || runtimeadmission.RejectUnknown(in) != nil || len(in.Views) > api.SidecarCapMax+1 {
		return Receipt{}, runtimeadmission.ErrInvalid
	}
	out := Receipt{Version: in.Version, InputHash: in.InputHash, SourcesHash: in.SourcesHash, TargetDir: in.TargetDir}
	for _, value := range in.Views {
		if value == nil || runtimeadmission.RejectUnknown(value) != nil {
			return Receipt{}, runtimeadmission.ErrInvalid
		}
		source, err := treeFromProto(value.SourceTree)
		if err != nil {
			return Receipt{}, err
		}
		projection, err := treeFromProto(value.ProjectionTree)
		if err != nil {
			return Receipt{}, err
		}
		out.Views = append(out.Views, View{WorkloadName: value.WorkloadName, SourceTree: source, ProjectionTree: projection})
	}
	return out, out.Check(expected)
}

func treeFromProto(in *vmmdpb.RuntimeScanTree) (scanview.Tree, error) {
	if in == nil || runtimeadmission.RejectUnknown(in) != nil || in.Entries <= 0 || in.Entries > api.ApplicationStandardRuntimeScanMaxEntries {
		return scanview.Tree{}, runtimeadmission.ErrInvalid
	}
	out := scanview.Tree{Version: in.Version, Digest: in.Digest, ProjectionDigest: in.ProjectionDigest, Entries: int(in.Entries), Bytes: in.Bytes}
	if !validTree(out) {
		return scanview.Tree{}, runtimeadmission.ErrInvalid
	}
	return out, nil
}

func (r Receipt) ToProto() *vmmdpb.MaterializeRuntimeScanResponse {
	out := &vmmdpb.MaterializeRuntimeScanResponse{Version: r.Version, InputHash: r.InputHash, SourcesHash: r.SourcesHash, TargetDir: r.TargetDir}
	for _, view := range r.Views {
		out.Views = append(out.Views, &vmmdpb.RuntimeScanView{WorkloadName: view.WorkloadName, SourceTree: treeToProto(view.SourceTree), ProjectionTree: treeToProto(view.ProjectionTree)})
	}
	return out
}

func treeToProto(t scanview.Tree) *vmmdpb.RuntimeScanTree {
	return &vmmdpb.RuntimeScanTree{Version: t.Version, Digest: t.Digest, ProjectionDigest: t.ProjectionDigest, Entries: int64(t.Entries), Bytes: t.Bytes}
}
