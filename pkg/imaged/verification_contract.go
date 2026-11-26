package imaged

// ADR-20261122220000: verification contracts. After the candidate passes its
// health and OpenAPI route checks, imaged runs the app's gregale.verify.yaml
// against it through one candidate-pinned session. A required check that
// fails or errors fails the deployment exactly like a failed smoke: the live
// pointer never moves and the predecessor keeps serving.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/verifycontract"
)

// verificationSessionGrace covers opening the candidate session (publishing
// the challenge and proving routing) on top of the contract's own budget.
const verificationSessionGrace = 15 * time.Second

// ContractVerification is what imaged needs to run verification contracts.
type ContractVerification struct {
	Verifier apihostingreceipt.Verifier
	Store    state.Store
	// Released decides the verification-contracts capability for the app's
	// account; nil releases nothing.
	Released func(platformflags.Subject) bool
	// QueryPoll is how often a SQL check's handoff row is read; zero is 250ms.
	QueryPoll time.Duration
	// Secrets opens the app secrets check headers reference (amendment 2);
	// nil makes a contract with references unavailable evidence.
	Secrets VerificationSecretOpener
	// NodeName is this node's name, recorded on the verification lease
	// secretd checks.
	NodeName string
}

// VerificationSecretOpener asks secretd for the app secrets a contract's
// check headers reference. missing lists names the deployment's scope does
// not hold.
type VerificationSecretOpener interface {
	OpenVerificationSecrets(ctx context.Context, appID, deploymentID string, keys []string) (values map[string]string, missing []string, err error)
}

// ApplyVerificationContract runs the app's contract, when it has one and the
// capability is released, against a candidate whose smoke already verified.
// It returns smoke with the report attached; a blocking report turns smoke
// into a failure. A contract that cannot be read or a session that cannot be
// opened is unavailable evidence, which the caller retries through its
// recovery window like any other unavailable smoke.
func ApplyVerificationContract(ctx context.Context, cv ContractVerification, app state.App, dep state.Deployment, smoke apihostingreceipt.SmokeResult) (apihostingreceipt.SmokeResult, error) {
	if smoke.Status != apihostingreceipt.SmokeVerified || cv.Released == nil {
		return smoke, nil
	}
	contracts, ok := cv.Store.(state.VerificationContractStore)
	if !ok {
		return smoke, nil
	}
	acct, err := state.AppResourceAccount(ctx, cv.Store, app)
	if err != nil {
		return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, fmt.Errorf("read account: %w", err))
	}
	if !cv.Released(platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: app.OrgID}) {
		return smoke, nil
	}
	stored, err := contracts.AppVerificationContract(ctx, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return smoke, nil
	}
	if err != nil {
		return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, err)
	}
	contract, err := verifycontract.ParseJSON(stored.Contract)
	if err != nil {
		// apid validated it on write; a stored contract that no longer
		// validates is the platform's problem, not the candidate's.
		return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, fmt.Errorf("stored verification contract: %w", err))
	}
	budget := time.Duration(contract.Timeout())*time.Second + verificationSessionGrace
	runCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	session, err := cv.Verifier.OpenCandidateSession(runCtx, app.Slug, dep.ID, HostingHealthPath(app, dep), budget+15*time.Second)
	if err != nil {
		if ctx.Err() != nil {
			return smoke, ctx.Err()
		}
		var publication *apihostingreceipt.ChallengePublicationError
		if errors.As(err, &publication) {
			return contractUnavailable(smoke, apihostingreceipt.SmokeErrorAuthorizationUnavailable, err)
		}
		return contractUnavailable(smoke, apihostingreceipt.SmokeErrorGatewayUnavailable, err)
	}
	exec := &contractExecutor{session: session, app: app, dep: dep, poll: cv.QueryPoll}
	if refs := contract.SecretRefs(); len(refs) > 0 {
		release, err := openVerificationSecrets(runCtx, cv, app, dep, refs, budget, exec)
		if err != nil {
			if ctx.Err() != nil {
				return smoke, ctx.Err()
			}
			return contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable, err)
		}
		defer release()
	}
	if queries, ok := cv.Store.(state.VerificationQueryStore); ok {
		exec.queries = queries
	}
	report := verifycontract.Run(runCtx, contract, exec, verifycontract.RunOptions{
		Production:     verificationIsProduction(app, dep),
		ContractSHA256: stored.SHA256,
	})
	if ctx.Err() != nil {
		// Shutdown is not the candidate's verdict.
		return smoke, ctx.Err()
	}
	smoke.VerificationContract = &report
	// A run whose only blocking checks could not reach the candidate or the
	// database is the platform's outage, not the candidate's verdict: retry
	// it through the hosting verification recovery window like other
	// unavailable evidence. Any failed assertion is the candidate's verdict.
	if report.Status != verifycontract.StatusPassed && platformOnlyFailure(report) {
		retry, err := contractUnavailable(smoke, apihostingreceipt.SmokeErrorContractUnavailable,
			fmt.Errorf("verification contract checks could not run: %s", strings.Join(report.Blocking(), ", ")))
		retry.VerificationContract = &report
		return retry, err
	}
	if report.Status != verifycontract.StatusPassed {
		smoke.Status = apihostingreceipt.SmokeFailed
		smoke.ErrorCode = apihostingreceipt.SmokeErrorVerificationContractFailed
		smoke.Error = "verification contract failed: " + strings.Join(report.Blocking(), ", ")
	}
	return smoke, nil
}

// verificationIsProduction reports whether the candidate serves production
// traffic and data: a production-scope deployment of an app that is not a PR
// preview. Environment branches, staging and other scopes are not.
func verificationIsProduction(app state.App, dep state.Deployment) bool {
	return state.IsProductionScope(dep.Scope) && !api.IsPreviewAppSlug(app.Slug)
}

func contractUnavailable(smoke apihostingreceipt.SmokeResult, code string, cause error) (apihostingreceipt.SmokeResult, error) {
	unavailable := &apihostingreceipt.VerificationUnavailableError{Code: code, Cause: cause}
	smoke.Status = apihostingreceipt.SmokeSkipped
	smoke.ErrorCode = code
	smoke.Error = unavailable.Error()
	return smoke, unavailable
}

// openVerificationSecrets records this node as the deployment's verifier and
// asks secretd for the referenced secrets. The returned release clears the
// values and ends the lease.
func openVerificationSecrets(ctx context.Context, cv ContractVerification, app state.App, dep state.Deployment, refs []string, budget time.Duration, exec *contractExecutor) (func(), error) {
	leases, ok := cv.Store.(state.VerificationLeaseStore)
	if cv.Secrets == nil || cv.NodeName == "" || !ok {
		return nil, errors.New("this node cannot open the secrets the verification contract references")
	}
	until := time.Now().Add(budget + api.VerificationLeaseGrace)
	if err := leases.PutDeploymentVerificationLease(ctx, dep.ID, app.ID, cv.NodeName, until); err != nil {
		return nil, fmt.Errorf("record verification lease: %w", err)
	}
	release := func() {
		clear(exec.secrets)
		// A lease that cannot be deleted expires on its own; secretd checks
		// the expiry.
		_ = leases.DeleteDeploymentVerificationLease(context.WithoutCancel(ctx), dep.ID)
	}
	values, _, err := cv.Secrets.OpenVerificationSecrets(ctx, app.ID, dep.ID, refs)
	if err != nil {
		release()
		return nil, fmt.Errorf("open verification secrets: %w", err)
	}
	exec.secrets = values
	return release, nil
}

type contractExecutor struct {
	session *apihostingreceipt.CandidateSession
	queries state.VerificationQueryStore
	app     state.App
	dep     state.Deployment
	poll    time.Duration
	// secrets holds the referenced app secrets; a reference it lacks is a
	// secret the deployment's scope does not hold.
	secrets map[string]string
}

func (e *contractExecutor) HTTP(ctx context.Context, check verifycontract.HTTPCheck) (verifycontract.HTTPResponse, error) {
	var body io.Reader
	contentType := ""
	switch {
	case check.JSON != nil:
		raw, err := json.Marshal(check.JSON)
		if err != nil {
			return verifycontract.HTTPResponse{}, &verifycontract.CheckError{Code: verifycontract.CodeRequestFailed, Message: "json body could not be encoded"}
		}
		body, contentType = bytes.NewReader(raw), "application/json"
	case check.Body != "":
		body = strings.NewReader(check.Body)
	}
	req, err := http.NewRequestWithContext(ctx, check.EffectiveMethod(), e.session.URL(check.Path), body)
	if err != nil {
		return verifycontract.HTTPResponse{}, &verifycontract.CheckError{Code: verifycontract.CodeRequestFailed, Message: "request could not be built"}
	}
	for name, value := range check.Headers {
		expanded, err := verifycontract.ExpandSecrets(value, e.secrets)
		if err != nil {
			// The message names the secret, never a value.
			return verifycontract.HTTPResponse{}, &verifycontract.CheckError{Code: verifycontract.CodeSecretMissing, Message: err.Error()}
		}
		req.Header.Set(name, expanded)
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	started := time.Now()
	resp, err := e.session.Client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return verifycontract.HTTPResponse{}, ctx.Err()
		}
		return verifycontract.HTTPResponse{}, &verifycontract.CheckError{Code: verifycontract.CodeRequestFailed, Message: requestFailure(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, verifycontract.MaxResponseBytes+1))
	latency := time.Since(started)
	if readErr != nil {
		return verifycontract.HTTPResponse{}, &verifycontract.CheckError{Code: verifycontract.CodeRequestFailed, Message: "the response body could not be read"}
	}
	truncated := len(raw) > verifycontract.MaxResponseBytes
	if truncated {
		raw = raw[:verifycontract.MaxResponseBytes]
	}
	return verifycontract.HTTPResponse{StatusCode: resp.StatusCode, Header: resp.Header, Body: raw, BodyTruncated: truncated, Latency: latency}, nil
}

func requestFailure(err error) string {
	if errors.Is(err, apihostingreceipt.ErrCandidateUnproven) {
		return "the response did not come from the candidate deployment"
	}
	return "the request did not reach the candidate"
}

func (e *contractExecutor) MCP(ctx context.Context, check verifycontract.MCPCheck, allowMutatingTools bool) error {
	client, err := mcphosting.NewClient(e.session.URL(check.EffectivePath()), "", "")
	if err != nil {
		return &verifycontract.CheckError{Code: verifycontract.CodeMCPFailed, Message: "the MCP endpoint URL is not usable"}
	}
	client.HTTP = e.session.Client()
	fixture := mcphosting.SmokeCase{Name: "contract", Arguments: check.Arguments, PromptArguments: check.PromptArguments,
		MIMEType: check.MIMEType, MessageRoles: check.MessageRoles}
	switch {
	case check.Tool != "":
		fixture.Kind, fixture.Target = "tool", check.Tool
		raw, err := json.Marshal(check.ExpectedResult)
		if err != nil {
			return &verifycontract.CheckError{Code: verifycontract.CodeMCPFailed, Message: "expected_result could not be encoded"}
		}
		fixture.ExpectedResult = raw
	case check.Resource != "":
		fixture.Kind, fixture.Target = "resource", check.Resource
	default:
		fixture.Kind, fixture.Target = "prompt", check.Prompt
	}
	err = mcphosting.RunContractCase(ctx, client, fixture, !allowMutatingTools)
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, mcphosting.ErrContractToolNotReadOnly):
		return &verifycontract.CheckError{Code: verifycontract.CodeMCPToolNotReadOnly, Message: "the tool does not declare readOnlyHint, so it runs only on previews and environment branches"}
	case errors.Is(err, mcphosting.ErrContractToolNotFound):
		return &verifycontract.CheckError{Code: verifycontract.CodeMCPFailed, Message: "the tool is not in the candidate's MCP catalog", Assertion: true}
	case errors.Is(err, mcphosting.ErrContractCaseFailed):
		return &verifycontract.CheckError{Code: verifycontract.CodeMCPFailed, Message: "the MCP operation failed or its result did not match", Assertion: true}
	case errors.Is(err, mcphosting.ErrContractIncompatible):
		return &verifycontract.CheckError{Code: verifycontract.CodeMCPFailed, Message: "the candidate's MCP endpoint did not initialize or list its catalog", Assertion: true}
	default:
		return &verifycontract.CheckError{Code: verifycontract.CodeMCPFailed, Message: "the MCP check could not run"}
	}
}

func (e *contractExecutor) SQL(ctx context.Context, name string, check verifycontract.SQLCheck) (verifycontract.SQLResult, error) {
	if e.queries == nil {
		return verifycontract.SQLResult{}, &verifycontract.CheckError{Code: verifycontract.CodeQueryUnavailable, Message: "SQL checks are not available on this node"}
	}
	expires := time.Now().Add(time.Minute)
	if deadline, ok := ctx.Deadline(); ok {
		expires = deadline
	}
	created, err := e.queries.CreateVerificationQuery(ctx, state.VerificationQuery{
		DeploymentID: e.dep.ID, AppID: e.app.ID, AccountID: e.app.AccountID,
		CheckName: name, Database: check.Database, Query: check.Query, ExpiresAt: expires,
	})
	if err != nil {
		if ctx.Err() != nil {
			return verifycontract.SQLResult{}, ctx.Err()
		}
		return verifycontract.SQLResult{}, &verifycontract.CheckError{Code: verifycontract.CodeQueryUnavailable, Message: "the SQL check could not be queued"}
	}
	poll := e.poll
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return verifycontract.SQLResult{}, ctx.Err()
		case <-ticker.C:
		}
		q, err := e.queries.VerificationQueryByID(ctx, created.ID)
		if err != nil {
			if ctx.Err() != nil {
				return verifycontract.SQLResult{}, ctx.Err()
			}
			continue
		}
		if q.Status != state.VerificationQueryDone {
			continue
		}
		switch q.ErrorCode {
		case "":
			return verifycontract.SQLResult{RowCount: q.RowCount, HasFirst: q.HasFirst, First: q.FirstValue}, nil
		case verifycontract.CodeDatabaseNotFound, verifycontract.CodeQueryFailed:
			// The contract names a database that does not exist or a query
			// PostgreSQL refused: the check's own verdict.
			return verifycontract.SQLResult{}, &verifycontract.CheckError{Code: q.ErrorCode, Message: q.Error, Assertion: true}
		default:
			return verifycontract.SQLResult{}, &verifycontract.CheckError{Code: q.ErrorCode, Message: q.Error}
		}
	}
}

// platformOnlyFailure reports whether every blocking check errored for a
// platform reason (the request or the database query could not run) rather
// than an answer of the candidate.
func platformOnlyFailure(report verifycontract.Report) bool {
	blocking := 0
	for _, c := range report.Checks {
		if !c.Required || (c.Status != verifycontract.StatusFailed && c.Status != verifycontract.StatusError) {
			continue
		}
		blocking++
		if c.Status != verifycontract.StatusError {
			return false
		}
		switch c.Code {
		case verifycontract.CodeRequestFailed, verifycontract.CodeQueryUnavailable:
		default:
			return false
		}
	}
	return blocking > 0
}
