package secretd

// adr: 20261117190000 — compute reaches the shared store only through
// per-object signed grants.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/storagegrant"
)

// ErrUnknownDeployment is returned by StorageGrantStore.DeploymentApp.
var ErrUnknownDeployment = errors.New("secretd: no such deployment")

// StorageGrantStore is what GrantStorageObjects reads besides Store: where a
// key's owner is placed, and the audit it appends to.
type StorageGrantStore interface {
	// DeploymentApp returns the deployment's app, or ErrUnknownDeployment.
	DeploymentApp(ctx context.Context, deploymentID string) (string, error)
	// NodeRunsApp reports whether nodeID holds an instance of appID that is
	// not over.
	NodeRunsApp(ctx context.Context, appID, nodeID string) (bool, error)
	// NodeBuildsDeployment reports whether nodeName claimed a running build
	// of deploymentID started since since.
	NodeBuildsDeployment(ctx context.Context, deploymentID, nodeName string, since time.Time) (bool, error)
	// NodeRunsSandboxOf reports whether nodeID runs a live sandbox session
	// of accountID.
	NodeRunsSandboxOf(ctx context.Context, accountID, nodeID string) (bool, error)
	// NodeRunsSandboxTemplateOf reports whether nodeID runs a live sandbox
	// session whose template appID builds.
	NodeRunsSandboxTemplateOf(ctx context.Context, appID, nodeID string) (bool, error)
	// NodeRunsJob reports whether nodeID runs a task instance of jobID that
	// is not over, or a live sandbox session that boots its image.
	NodeRunsJob(ctx context.Context, jobID, nodeID string) (bool, error)
	// RecordStorageGrant appends one audit row.
	RecordStorageGrant(ctx context.Context, r StorageGrantRecord) error
}

// StorageGrantRecord is one storage_grant_audit row: the objects of one
// subject one call granted for one access.
type StorageGrantRecord struct {
	NodeID    string
	Class     string
	SubjectID string
	AppID     string
	Access    string
	Objects   int
}

const (
	accessRead  = "read"
	accessWrite = "write"
)

// storageGrants is GrantStorageObjects' configuration; nil refuses it.
type storageGrants struct {
	store  StorageGrantStore
	signer storagegrant.Signer
}

// WithStorageGrants turns on GrantStorageObjects (ADR-20261117190000 slice
// 2): placements come from store, URLs are signed by signer with the
// control plane's store credentials.
func (s *Server) WithStorageGrants(store StorageGrantStore, signer storagegrant.Signer) *Server {
	if store != nil && signer != nil {
		s.storage = &storageGrants{store: store, signer: signer}
	}
	return s
}

// storageSubject groups a request's keys by owner and access.
type storageSubject struct {
	subject storagegrant.Subject
	access  string
}

// GrantStorageObjects implements secretdpb.SecretdServer.
func (s *Server) GrantStorageObjects(ctx context.Context, req *secretdpb.GrantStorageObjectsRequest) (*secretdpb.GrantStorageObjectsResponse, error) {
	if s.storage == nil {
		return nil, rpcErr(codes.FailedPrecondition, "secretd does not grant storage objects")
	}
	name, nodeID, err := s.caller(ctx, rpcGrantStorageObjects)
	if err != nil {
		return nil, err
	}
	objects := req.GetObjects()
	if len(objects) == 0 || len(objects) > storagegrant.MaxKeysPerRequest {
		return nil, rpcErr(codes.InvalidArgument, fmt.Sprintf("secretd needs 1 to %d objects", storagegrant.MaxKeysPerRequest))
	}
	requests := make([]storagegrant.Request, len(objects))
	counts := map[storageSubject]int{}
	var order []storageSubject
	for i, o := range objects {
		r := storagegrant.Request{Key: o.GetKey(), Op: storagegrant.Op(o.GetOp()), Generation: o.GetGeneration(),
			ETag: o.GetEtag(), UploadID: o.GetUploadId(), PartNumber: o.GetPartNumber(),
			ContentType: o.GetContentType(), Metadata: o.GetMetadata()}
		if err := r.Validate(); err != nil {
			s.metrics.refuse(rpcGrantStorageObjects, ReasonNotAuthorized)
			return nil, rpcErr(codes.InvalidArgument, "secretd refused object "+fmt.Sprint(i)+": "+err.Error())
		}
		requests[i] = r
		access := accessRead
		if r.Op.Writes() {
			access = accessWrite
		}
		key := storageSubject{subject: storagegrant.Resolve(r.Key), access: access}
		// Build outputs are created by nodes, never replaced. Platform
		// objects may be replaced, but only conditioned on the current
		// object (Validate requires it), so no update is lost
		// (ADR-20261117190000 amendment 1).
		if access == accessWrite && createOnlyClass(key.subject.Class) && replaces(r) {
			s.log.Warn("secretd: refused storage grant", "node", logsanitize.Field(name), "class", string(key.subject.Class),
				"key", logsanitize.Field(r.Key), "why", "replace of a create-only object")
			s.metrics.refuse(rpcGrantStorageObjects, ReasonNotAuthorized)
			return nil, rpcErr(codes.PermissionDenied, "secretd refused: nodes create "+string(key.subject.Class)+" objects but never replace them")
		}
		if access == accessWrite && key.subject.Class == storagegrant.ClassPlatform {
			// Shared objects every node reads: name each one a node writes.
			s.log.Info("secretd: platform object write requested", "node", logsanitize.Field(name), "op", string(r.Op), "key", logsanitize.Field(r.Key))
		}
		if counts[key] == 0 {
			order = append(order, key)
		}
		counts[key]++
	}
	records := make([]StorageGrantRecord, 0, len(order))
	for _, sub := range order {
		appID, err := s.admitStorage(ctx, name, nodeID, sub)
		if err != nil {
			for _, refused := range order {
				s.metrics.storageGrant(string(refused.subject.Class), refused.access, "refused", counts[refused])
			}
			return nil, err
		}
		// Platform reads are only counted; platform writes are recorded
		// like tenant grants, under the nil subject (amendment 1).
		if sub.subject.Class != storagegrant.ClassPlatform || sub.access == accessWrite {
			subjectID := sub.subject.ID
			if sub.subject.Class == storagegrant.ClassPlatform {
				subjectID = platformSubjectID
			}
			records = append(records, StorageGrantRecord{NodeID: nodeID, Class: string(sub.subject.Class),
				SubjectID: subjectID, AppID: appID, Access: sub.access, Objects: counts[sub]})
		}
	}
	// The record precedes the grant: a grant that cannot be recorded is
	// not signed.
	for _, r := range records {
		if err := s.storage.store.RecordStorageGrant(ctx, r); err != nil {
			s.log.Error("secretd: record storage grant", "node", logsanitize.Field(name), "subject", logsanitize.Field(r.SubjectID), "err", err)
			return nil, rpcErr(codes.Unavailable, "secretd could not record the grant")
		}
	}
	expires := s.now().Add(storagegrant.TTL)
	resp := &secretdpb.GrantStorageObjectsResponse{Grants: make([]*secretdpb.StorageObjectGrant, len(requests))}
	for i, r := range requests {
		g, err := s.storage.signer.Sign(ctx, r, expires)
		if errors.Is(err, storagegrant.ErrUnsupported) {
			return nil, rpcErr(codes.FailedPrecondition, "secretd cannot sign that operation for this store")
		}
		if err != nil {
			s.log.Error("secretd: sign storage grant", "node", logsanitize.Field(name), "key", logsanitize.Field(r.Key), "err", err)
			return nil, rpcErr(codes.Unavailable, "secretd could not sign the grant")
		}
		resp.Grants[i] = &secretdpb.StorageObjectGrant{Key: r.Key, Method: g.Method, Url: g.URL, Headers: g.Headers,
			ExpiresAt: g.Expires.UTC().Format(time.RFC3339)}
	}
	for _, sub := range order {
		s.metrics.storageGrant(string(sub.subject.Class), sub.access, "granted", counts[sub])
	}
	return resp, nil
}

// platformSubjectID is the audit subject of platform objects, which belong
// to no tenant.
const platformSubjectID = "00000000-0000-0000-0000-000000000000"

// admitStorage decides one subject for the caller and returns the app it
// belongs to, if any. Every refusal is PermissionDenied and logged.
func (s *Server) admitStorage(ctx context.Context, name, nodeID string, sub storageSubject) (string, error) {
	refuse := func(why string) (string, error) {
		s.log.Warn("secretd: refused storage grant", "node", logsanitize.Field(name), "class", string(sub.subject.Class),
			"subject", logsanitize.Field(sub.subject.ID), "access", sub.access, "why", why)
		s.metrics.refuse(rpcGrantStorageObjects, ReasonNotAuthorized)
		return "", rpcErr(codes.PermissionDenied, "secretd refused: "+why)
	}
	lookupFailed := func(what string, err error) (string, error) {
		s.log.Error("secretd: storage grant lookup", "what", what, "subject", logsanitize.Field(sub.subject.ID), "err", err)
		return "", rpcErr(codes.Unavailable, "secretd could not look up the "+what)
	}
	var appID, budgetKey string
	switch sub.subject.Class {
	case storagegrant.ClassPlatform:
		// Any node may write a platform object: create, or replace the
		// object it read. Readers verify bases and kernels against signd.
		return "", nil
	case storagegrant.ClassBuildOutput:
		run, err := s.store.BuildRunner(ctx, sub.subject.ID)
		if errors.Is(err, ErrUnknownBuild) {
			return refuse("the object's build does not exist")
		}
		if err != nil {
			return lookupFailed("build", err)
		}
		if run.Node == "" || run.Node != name || !s.now().Before(run.StartedAt.Add(buildRunWindow)) {
			return refuse("the caller did not run the object's build")
		}
		appID, budgetKey = run.AppID, run.AppID
	case storagegrant.ClassDeployment:
		var err error
		appID, err = s.storage.store.DeploymentApp(ctx, sub.subject.ID)
		if errors.Is(err, ErrUnknownDeployment) {
			return refuse("the object's deployment does not exist")
		}
		if err != nil {
			return lookupFailed("deployment", err)
		}
		ok, err := s.deploymentPlaced(ctx, name, nodeID, appID, sub.subject.ID, sub.access)
		if err != nil {
			return lookupFailed("placement", err)
		}
		if !ok {
			return refuse("the object's app is not placed on the caller")
		}
		budgetKey = appID
	case storagegrant.ClassSandboxAccount:
		ok, err := s.storage.store.NodeRunsSandboxOf(ctx, sub.subject.ID, nodeID)
		if err != nil {
			return lookupFailed("sandbox session", err)
		}
		if !ok {
			return refuse("the caller runs no sandbox session of the object's account")
		}
		budgetKey = "sandbox:" + sub.subject.ID
	case storagegrant.ClassBuild:
		if sub.access == accessWrite {
			return refuse("no node writes build sources")
		}
		run, err := s.store.BuildRunner(ctx, sub.subject.ID)
		if errors.Is(err, ErrUnknownBuild) {
			return refuse("the object's build does not exist")
		}
		if err != nil {
			return lookupFailed("build", err)
		}
		if !run.Running || run.Node == "" || run.Node != name || !s.now().Before(run.StartedAt.Add(buildRunWindow)) {
			return refuse("the caller is not running the object's build")
		}
		appID, budgetKey = run.AppID, run.AppID
	case storagegrant.ClassJob:
		ok, err := s.jobPlaced(ctx, name, nodeID, sub.subject.ID)
		if err != nil {
			return lookupFailed("job", err)
		}
		if !ok {
			return refuse("the caller neither materializes nor runs the object's job")
		}
		budgetKey = "job:" + sub.subject.ID
	default:
		return refuse("the key is outside the granted keyspace")
	}
	if !s.budget.allow(nodeID, budgetKey, s.now()) {
		s.log.Error("secretd: node over its distinct-app budget", "node", logsanitize.Field(name), "subject", logsanitize.Field(budgetKey))
		s.metrics.refuse(rpcGrantStorageObjects, ReasonBudgetExhausted)
		return "", rpcErr(codes.ResourceExhausted, "secretd refused: the caller opened too many distinct apps this hour")
	}
	return appID, nil
}

// deploymentPlaced: the node owns the app or holds a grant for it, runs an
// instance of it, prepares one of its images, runs a build of this
// deployment, or (reads only) runs a sandbox session of a template the app
// builds.
func (s *Server) deploymentPlaced(ctx context.Context, name, nodeID, appID, deploymentID, access string) (bool, error) {
	if target, err := s.store.AppTarget(ctx, appID); err == nil {
		if _, err := s.authorize(ctx, appID, nodeID, target); err == nil {
			return true, nil
		}
	}
	if ok, err := s.storage.store.NodeRunsApp(ctx, appID, nodeID); err != nil || ok {
		return ok, err
	}
	if ok, err := s.store.PreparesImage(ctx, appID, name, s.now().Add(-imagePreparationWindow)); err != nil || ok {
		return ok, err
	}
	if access == accessRead {
		// A sandbox session boots its template app's layer.
		if ok, err := s.storage.store.NodeRunsSandboxTemplateOf(ctx, appID, nodeID); err != nil || ok {
			return ok, err
		}
	}
	return s.storage.store.NodeBuildsDeployment(ctx, deploymentID, name, s.now().Add(-buildRunWindow))
}

// jobPlaced: the node holds the job's live materialization lease or runs
// one of its task instances.
func (s *Server) jobPlaced(ctx context.Context, name, nodeID, jobID string) (bool, error) {
	owner, until, err := s.store.JobMaterializationLease(ctx, jobID)
	switch {
	case errors.Is(err, ErrUnknownJob):
		// No lease row: only a task instance can place the job here.
	case err != nil:
		return false, err
	case strings.HasPrefix(owner, name+"/") && s.now().Before(until):
		return true, nil
	}
	return s.storage.store.NodeRunsJob(ctx, jobID, nodeID)
}

// createOnlyClass: nodes may create these objects but never replace them
// (build outputs; platform objects allow conditioned replaces).
func createOnlyClass(c storagegrant.Class) bool {
	return c == storagegrant.ClassBuildOutput
}

// replaces reports whether r overwrites an existing object: a replace, or
// a multipart completion conditioned on the current object.
func replaces(r storagegrant.Request) bool {
	return r.Op == storagegrant.OpReplace || (r.Op == storagegrant.OpCompleteMultipart && (r.ETag != "" || r.Generation != 0))
}
