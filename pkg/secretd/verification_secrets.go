package secretd

// ADR-20261122220000 amendment 2: a verification contract's HTTP check
// headers may reference the app's own secrets as ${secret:NAME}. imaged, on
// whichever compute node took the deployment's activation, writes a
// verification lease naming itself, then asks here for the referenced names.
// secretd opens them only for that node, only while the lease is unexpired
// and the deployment is still a candidate, only names the app's stored
// contract references, and only from the deployment's own scope. The values
// go to the app's candidate, which already holds them as env.

import (
	"context"
	"errors"
	"sort"
	"time"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/verifycontract"
	"google.golang.org/grpc/codes"
)

// ErrUnknownVerificationLease is returned for a deployment no node verifies.
var ErrUnknownVerificationLease = errors.New("no verification lease for the deployment")

// VerificationLease is the node verifying a deployment and what secretd
// checks before opening the secrets its contract references.
type VerificationLease struct {
	AppID     string
	NodeName  string
	ExpiresAt time.Time
	// Status and Scope are the deployment's.
	Status string
	Scope  string
	// Contract is the app's stored contract JSON; nil when it holds none.
	Contract []byte
	// ProbeSecret is the key the app's MCP listing names for the tool
	// contract gate (ADR-20261127191645); "" when it names none.
	ProbeSecret string
}

// VerificationLeaseStore is the optional Store extension
// OpenVerificationSecrets reads.
type VerificationLeaseStore interface {
	// VerificationLease returns deploymentID's lease, or
	// ErrUnknownVerificationLease.
	VerificationLease(ctx context.Context, deploymentID string) (VerificationLease, error)
}

// candidateStatus reports whether a deployment in status may still be
// verified: neither live nor terminal.
func candidateStatus(status string) bool {
	switch status {
	case "pending", "building", "imaging", "snapshotting":
		return true
	}
	return false
}

// OpenVerificationSecrets implements secretdpb.SecretdServer.
func (s *Server) OpenVerificationSecrets(ctx context.Context, req *secretdpb.OpenVerificationSecretsRequest) (*secretdpb.OpenVerificationSecretsResponse, error) {
	appID, deploymentID, keys := req.GetAppId(), req.GetDeploymentId(), req.GetKeys()
	if appID == "" || deploymentID == "" || len(keys) == 0 || len(keys) > api.VerificationContractMaxSecretRefs {
		return nil, rpcErr(codes.InvalidArgument, "secretd needs an app id, a deployment id and 1 to 8 secret keys")
	}
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if api.ValidateEnvKey(key) != nil || seen[key] {
			return nil, rpcErr(codes.InvalidArgument, "secretd needs distinct valid secret keys")
		}
		seen[key] = true
	}
	leases, ok := s.store.(VerificationLeaseStore)
	if !ok || len(s.identities) == 0 {
		return nil, rpcErr(codes.FailedPrecondition, "secretd cannot open verification secrets")
	}
	name, nodeID, err := s.caller(ctx, rpcOpenVerificationSecrets)
	if err != nil {
		return nil, err
	}
	lease, err := s.verificationLease(ctx, leases, name, appID, deploymentID, keys)
	if err != nil {
		return nil, err
	}
	target, err := s.store.AppTarget(ctx, appID)
	if err != nil {
		return nil, rpcErr(codes.NotFound, "secretd found no such app")
	}
	if !s.budget.allow(nodeID, appID, s.now()) {
		s.log.Error("secretd: node over its distinct-app budget", "node", logsanitize.Field(name), "app_id", logsanitize.Field(appID))
		s.metrics.refuse(rpcOpenVerificationSecrets, ReasonBudgetExhausted)
		return nil, rpcErr(codes.ResourceExhausted, "secretd refused: the caller opened too many distinct apps this hour")
	}
	values, missing, err := s.openVerificationSecrets(ctx, appID, lease.Scope, target.AccountID, keys)
	if err != nil {
		s.log.Warn("secretd: refused verification secret envelope", "node", logsanitize.Field(name), "app_id", logsanitize.Field(appID), "err", err)
		s.metrics.refuse(rpcOpenVerificationSecrets, envelopeReason(err))
		return nil, rpcErr(codes.FailedPrecondition, "secretd refused an envelope: "+err.Error())
	}
	if len(values) > 0 {
		// The lease is the scheduler's claim on this work, recorded as a grant
		// like a build's or a preparation's.
		if err := s.store.RecordOpen(ctx, nodeID, appID, "grant", len(values)); err != nil {
			clear(values)
			s.log.Error("secretd: record open", "node", logsanitize.Field(name), "app_id", logsanitize.Field(appID), "err", err)
			return nil, rpcErr(codes.Unavailable, "secretd could not record the open")
		}
	}
	s.metrics.open(rpcOpenVerificationSecrets, "grant")
	return &secretdpb.OpenVerificationSecretsResponse{Values: values, Missing: missing}, nil
}

// verificationLease admits name only while it holds deploymentID's unexpired
// lease for appID, the deployment is still a candidate and every key is
// referenced by the app's stored contract or is the MCP listing's probe
// secret (ADR-20261127191645).
func (s *Server) verificationLease(ctx context.Context, leases VerificationLeaseStore, name, appID, deploymentID string, keys []string) (VerificationLease, error) {
	refuse := func(reason, msg string) (VerificationLease, error) {
		s.log.Warn("secretd: refused verification secrets", "node", logsanitize.Field(name), "app_id", logsanitize.Field(appID),
			"deployment_id", logsanitize.Field(deploymentID), "reason", reason)
		s.metrics.refuse(rpcOpenVerificationSecrets, ReasonNotAuthorized)
		return VerificationLease{}, rpcErr(codes.PermissionDenied, "secretd refused: "+msg)
	}
	lease, err := leases.VerificationLease(ctx, deploymentID)
	if errors.Is(err, ErrUnknownVerificationLease) {
		return refuse("no lease", "the caller does not verify that deployment")
	}
	if err != nil {
		s.log.Error("secretd: verification lease lookup", "deployment_id", logsanitize.Field(deploymentID), "err", err)
		return VerificationLease{}, rpcErr(codes.Unavailable, "secretd could not look up the verification lease")
	}
	if lease.AppID != appID || lease.NodeName != name || !s.now().Before(lease.ExpiresAt) {
		return refuse("lease mismatch", "the caller does not verify that deployment")
	}
	if !candidateStatus(lease.Status) {
		return refuse("not a candidate", "the deployment is no longer a candidate")
	}
	var contractKeys []string
	for _, key := range keys {
		if lease.ProbeSecret == "" || key != lease.ProbeSecret {
			contractKeys = append(contractKeys, key)
		}
	}
	if len(contractKeys) == 0 {
		return lease, nil
	}
	if lease.Contract == nil {
		return refuse("no contract", "the app holds no verification contract")
	}
	contract, err := verifycontract.ParseJSON(lease.Contract)
	if err != nil {
		return refuse("contract invalid", "the app's verification contract does not validate")
	}
	refs := contract.SecretRefs()
	for _, key := range contractKeys {
		if i := sort.SearchStrings(refs, key); i >= len(refs) || refs[i] != key {
			return refuse("unreferenced key", "the app's verification contract does not reference "+key)
		}
	}
	return lease, nil
}

// openVerificationSecrets opens each key from appID's app_secrets row in
// scope. A key the scope does not hold is missing, not an error: the contract
// names a secret the customer has not set, which is the candidate's verdict.
func (s *Server) openVerificationSecrets(ctx context.Context, appID, scope, accountID string, keys []string) (map[string]string, []string, error) {
	values := make(map[string]string, len(keys))
	var missing []string
	for _, key := range keys {
		opened, err := s.open(ctx, appID, []*secretdpb.SealedEntry{{Key: key, Scope: scope}}, accountID)
		if errors.Is(err, ErrUnknownSecret) {
			missing = append(missing, key)
			continue
		}
		if err != nil {
			clear(values)
			return nil, nil, err
		}
		values[key] = opened[key]
	}
	return values, missing, nil
}
