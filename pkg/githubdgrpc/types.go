// types.go holds the platform-friendly enum + struct mirrors of the
// githubd proto wire types. Callers (apid) import githubdgrpc and
// never reach into githubdpb directly — keeps the proto package
// confined to this one. Mirrors scheddgrpc.types (ADR-018).
package githubdgrpc

// InstallState is the per-account GitHub App install lifecycle. Maps
// to githubdpb.InstallState. Mirrors the dashboard "Connect GitHub"
// state machine (UX spec §5.1).
type InstallState int32

const (
	InstallStateUnspecified  InstallState = 0
	InstallStateNotInstalled InstallState = 1
	InstallStateInstalling   InstallState = 2
	InstallStateInstalled    InstallState = 3
	InstallStateBound        InstallState = 4
)

// CheckPhase is the githubd-side build-progress state machine. Maps to
// githubdpb.CheckPhase. pkg/githubd/checks.go turns these into GitHub
// status + conclusion values.
type CheckPhase int32

const (
	CheckPhaseUnspecified CheckPhase = 0
	CheckPhaseQueued      CheckPhase = 1
	CheckPhaseBuilding    CheckPhase = 2
	CheckPhaseLive        CheckPhase = 3
	CheckPhaseFailed      CheckPhase = 4
)

// Repo is one repo in the installation's catalog. The dashboard's
// repo picker renders these.
type Repo struct {
	FullName      string // "owner/name"
	DefaultBranch string // "main" | "master" | custom
	Private       bool
}

// AppBinding is the (app, repo, branch) row that drives webhook
// dispatch. Returned by GetAppBinding; empty fields mean "not bound".
type AppBinding struct {
	RepoFullName     string
	ProductionBranch string
	BindingID        string
}
