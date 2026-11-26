package state

import (
	"context"
	"time"
)

// DataAPIInspectionFence captures monotonic database revisions before the
// provider derives the private target expectation.
type DataAPIInspectionFence struct {
	BindingRevision, DatabaseRevision, RuntimeGeneration int64
}

// DataAPIInspectionAdmission is private control-plane input. The provider
// expectation is checked against the same database rows as the admission.
type DataAPIInspectionAdmission struct {
	AccountID, AppID, DeploymentID, DatabaseID, BindingID string
	Table, Nonce, TargetDigest, TrustDigest               string
	CredentialGeneration                                  int64
	CredentialRef, ProviderIdentityID, DatabaseResourceID string
	BackendID, BackendFingerprint                         string
	BindingUpdatedAt, DatabaseUpdatedAt                   time.Time
	DatabaseDesiredGeneration, DatabaseObservedGeneration int64
	Fence                                                 DataAPIInspectionFence
	CreatedAt                                             time.Time
}

// DataAPIInspectionRecord is internal to the control plane. Its output must
// not be treated as verified until the execution and provider checks pass.
type DataAPIInspectionRecord struct {
	TaskID, AppID, DeploymentID, DatabaseID, BindingID string
	Command                                            []string
	Status                                             AppTaskStatus
	Stdout                                             string
	OutputTruncated                                    bool
	ExitCode                                           *int
	CredentialGeneration                               int64
	TargetDigest, TrustDigest                          string
	Current                                            bool
	FinishedAt                                         *time.Time
	ProtocolVersion                                    uint16
	StdoutSHA256                                       []byte
}

type DataAPIInspectionAdmissionStore interface {
	ReadDataAPIInspectionFence(context.Context, string, string, string, string, string) (DataAPIInspectionFence, error)
	AdmitDataAPIInspection(context.Context, DataAPIInspectionAdmission) (string, error)
	ReadDataAPIInspectionRecord(context.Context, string, string, string) (DataAPIInspectionRecord, error)
}
