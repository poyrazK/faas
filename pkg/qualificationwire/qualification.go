// Package qualificationwire preserves the private ADR-568 execution capability
// across schedd/vmmd RPCs. It never resolves a new owner from current intent.
package qualificationwire

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const contractVersion = 1

func validUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && (id.String() == value || strings.ReplaceAll(id.String(), "-", "") == value)
}

func validateExecution(frame state.EnvironmentQualificationExecution) error {
	if frame.CaptureInstanceID != "" {
		return fmt.Errorf("qualification capture protocol cannot carry restore authority: %w", state.ErrInvalidArgument)
	}
	for _, value := range []string{frame.InstanceID, frame.RequestID, frame.GraphID, frame.AppID, frame.DeploymentID, frame.NodeID,
		frame.WakeID, frame.SourceID, frame.EnvironmentID, frame.RevisionID, frame.CleanupToken} {
		if !validUUID(value) {
			return fmt.Errorf("qualification execution identity is incomplete: %w", state.ErrInvalidArgument)
		}
	}
	name, workload := strings.CutPrefix(frame.Resource, "workload/")
	digest, err := hex.DecodeString(frame.PlanHash)
	if !workload || !api.ValidAppSlug(name) || !api.ValidProjectEnvironmentSlug(frame.Scope) || frame.Generation < 1 || frame.IntentVersion < 0 ||
		frame.Attempt < 1 || frame.RAMMB <= 0 || frame.RAMMB > math.MaxInt32 || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != frame.PlanHash ||
		frame.Artifact.RootfsBytes <= 0 || frame.Artifact.RootfsKey == "" && frame.Artifact.RootfsPath == "" {
		return fmt.Errorf("qualification execution contract is incomplete: %w", state.ErrInvalidArgument)
	}
	switch frame.Artifact.Kind {
	case state.DeploymentKindImage, state.DeploymentKindTarball, state.DeploymentKindDockerfile, state.DeploymentKindGitHub, state.DeploymentKindPreview:
		return nil
	default:
		return fmt.Errorf("qualification artifact kind is unsupported: %w", state.ErrInvalidArgument)
	}
}

// ExecutionToProto validates without changing UUID spelling or artifact inputs.
// CleanupToken travels only on this private authenticated transport.
func ExecutionToProto(frame state.EnvironmentQualificationExecution) (*vmmdpb.EnvironmentQualificationExecution, error) {
	if err := validateExecution(frame); err != nil {
		return nil, err
	}
	a := frame.Artifact
	return &vmmdpb.EnvironmentQualificationExecution{
		ContractVersion: contractVersion, InstanceId: frame.InstanceID, RequestId: frame.RequestID, GraphId: frame.GraphID, AppId: frame.AppID,
		DeploymentId: frame.DeploymentID, NodeId: frame.NodeID, WakeId: frame.WakeID, SourceId: frame.SourceID, EnvironmentId: frame.EnvironmentID,
		RevisionId: frame.RevisionID, Resource: frame.Resource, Scope: frame.Scope, PlanHash: frame.PlanHash, Generation: frame.Generation,
		IntentVersion: frame.IntentVersion, Attempt: frame.Attempt, RamMb: int32(frame.RAMMB), CleanupToken: frame.CleanupToken,
		Artifact: &vmmdpb.EnvironmentQualificationArtifact{RootfsPath: a.RootfsPath, RootfsKey: a.RootfsKey, RootfsBytes: a.RootfsBytes,
			ImageDigest: a.ImageDigest, BuildId: a.BuildID, Kind: string(a.Kind), CommitSha: a.CommitSHA},
	}, nil
}

// ExecutionFromProto rejects unknown capability fields rather than dropping
// authority that this version cannot fence. Missing/version-zero is not legacy.
func ExecutionFromProto(p *vmmdpb.EnvironmentQualificationExecution) (state.EnvironmentQualificationExecution, error) {
	var frame state.EnvironmentQualificationExecution
	if p == nil || p.GetContractVersion() != contractVersion || p.GetArtifact() == nil || len(p.ProtoReflect().GetUnknown()) != 0 ||
		len(p.GetArtifact().ProtoReflect().GetUnknown()) != 0 {
		return frame, fmt.Errorf("qualification execution wire profile is unsupported: %w", state.ErrInvalidArgument)
	}
	a := p.GetArtifact()
	frame = state.EnvironmentQualificationExecution{InstanceID: p.GetInstanceId(), RequestID: p.GetRequestId(), GraphID: p.GetGraphId(), AppID: p.GetAppId(),
		DeploymentID: p.GetDeploymentId(), NodeID: p.GetNodeId(), WakeID: p.GetWakeId(), SourceID: p.GetSourceId(), EnvironmentID: p.GetEnvironmentId(),
		RevisionID: p.GetRevisionId(), Resource: p.GetResource(), Scope: p.GetScope(), PlanHash: p.GetPlanHash(), Generation: p.GetGeneration(),
		IntentVersion: p.GetIntentVersion(), Attempt: p.GetAttempt(), RAMMB: int(p.GetRamMb()), CleanupToken: p.GetCleanupToken(),
		Artifact: state.EnvironmentWorkloadArtifact{RootfsPath: a.GetRootfsPath(), RootfsKey: a.GetRootfsKey(), RootfsBytes: a.GetRootfsBytes(),
			ImageDigest: a.GetImageDigest(), BuildID: a.GetBuildId(), Kind: state.DeploymentKind(a.GetKind()), CommitSHA: a.GetCommitSha()}}
	return frame, validateExecution(frame)
}

func validateNativeRetirement(proof state.EnvironmentQualificationRetirement) error {
	if proof.Kind != state.QualificationNativeRetired || !proof.ProcessesExited || !proof.ResourcesRemoved {
		return fmt.Errorf("qualification has no physical retirement proof: %w", state.ErrConflict)
	}
	for _, value := range []string{proof.ReceiptID, proof.NativeGeneration, proof.KernelBootID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return fmt.Errorf("qualification retirement identity is incomplete: %w", state.ErrConflict)
		}
	}
	return nil
}

func NativeRetirementToProto(proof state.EnvironmentQualificationRetirement) (*vmmdpb.EnvironmentQualificationRetirement, error) {
	if err := validateNativeRetirement(proof); err != nil {
		return nil, err
	}
	return &vmmdpb.EnvironmentQualificationRetirement{Kind: proof.Kind, ReceiptId: proof.ReceiptID, NativeGeneration: proof.NativeGeneration,
		KernelBootId: proof.KernelBootID, ProcessesExited: proof.ProcessesExited, ResourcesRemoved: proof.ResourcesRemoved}, nil
}

func NativeRetirementFromProto(p *vmmdpb.EnvironmentQualificationRetirement) (state.EnvironmentQualificationRetirement, error) {
	if p == nil || len(p.ProtoReflect().GetUnknown()) != 0 {
		return state.EnvironmentQualificationRetirement{}, fmt.Errorf("qualification retirement wire profile is unsupported: %w", state.ErrConflict)
	}
	proof := state.EnvironmentQualificationRetirement{Kind: p.GetKind(), ReceiptID: p.GetReceiptId(), NativeGeneration: p.GetNativeGeneration(),
		KernelBootID: p.GetKernelBootId(), ProcessesExited: p.GetProcessesExited(), ResourcesRemoved: p.GetResourcesRemoved()}
	if err := validateNativeRetirement(proof); err != nil {
		return state.EnvironmentQualificationRetirement{}, err
	}
	return proof, nil
}
