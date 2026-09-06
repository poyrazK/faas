package neon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type roleResponse struct {
	Role struct {
		Name string `json:"name"`
	} `json:"role"`
	Operations []operation `json:"operations"`
}

type createRoleRequest struct {
	Role struct {
		Name string `json:"name"`
	} `json:"role"`
}

type connectionURIResponse struct {
	URI string `json:"uri"`
}

type parsedConnection struct {
	username string
	password string
	database string
	tlsMode  string
	host     string
	port     uint16
}

func (p *Provider) IssueCredentials(ctx context.Context, request managedpostgres.CredentialRequest) (managedpostgres.CredentialMaterial, error) {
	if err := validateCredentialRequest(request); err != nil {
		return managedpostgres.CredentialMaterial{}, err
	}
	if request.Access != managedpostgres.CredentialReadWrite {
		return managedpostgres.CredentialMaterial{}, managedpostgres.ErrUnsupported
	}
	branchID, err := p.defaultBranch(ctx, request.ProviderResourceID)
	if err != nil {
		return managedpostgres.CredentialMaterial{}, err
	}
	roleName := p.roleName(request.ProviderResourceID, request.IdentityKey)
	if err := p.ensureRole(ctx, request.ProviderResourceID, branchID, roleName); err != nil {
		return managedpostgres.CredentialMaterial{}, err
	}
	return p.credentialMaterial(ctx, request.ProviderResourceID, branchID, roleName)
}

func (p *Provider) RevokeCredentials(ctx context.Context, request managedpostgres.CredentialRequest) error {
	if err := validateCredentialRequest(request); err != nil {
		return err
	}
	branchID, err := p.defaultBranch(ctx, request.ProviderResourceID)
	if err != nil {
		if errors.Is(err, managedpostgres.ErrNotFound) {
			return nil
		}
		return err
	}
	roleName := p.roleName(request.ProviderResourceID, request.IdentityKey)
	path := "/projects/" + url.PathEscape(request.ProviderResourceID) + "/branches/" + url.PathEscape(branchID) + "/roles/" + url.PathEscape(roleName)
	var response roleResponse
	err = p.doJSON(ctx, http.MethodDelete, path, nil, nil, &response, http.StatusOK, http.StatusNoContent)
	if errors.Is(err, managedpostgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return p.waitForOperations(ctx, request.ProviderResourceID, response.Operations)
}

func validateCredentialRequest(request managedpostgres.CredentialRequest) error {
	if !validProviderID.MatchString(request.ProviderResourceID) || request.IdentityKey == "" || len(request.IdentityKey) > 1024 || request.IdempotencyKey == "" || len(request.IdempotencyKey) > 255 {
		return managedpostgres.ErrInvalid
	}
	if request.Access != managedpostgres.CredentialReadWrite && request.Access != managedpostgres.CredentialReadOnly {
		return managedpostgres.ErrInvalid
	}
	return nil
}

func (p *Provider) defaultBranch(ctx context.Context, projectID string) (string, error) {
	path := "/projects/" + url.PathEscape(projectID) + "/branches"
	var response branchesResponse
	if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &response, http.StatusOK); err != nil {
		return "", err
	}
	branchID := ""
	for _, candidate := range response.Branches {
		if !candidate.Default {
			continue
		}
		if branchID != "" || !validProviderID.MatchString(candidate.ID) {
			return "", managedpostgres.ErrUnavailable
		}
		branchID = candidate.ID
	}
	if branchID == "" {
		return "", managedpostgres.ErrUnavailable
	}
	return branchID, nil
}

func (p *Provider) ensureRole(ctx context.Context, projectID, branchID, roleName string) error {
	path := "/projects/" + url.PathEscape(projectID) + "/branches/" + url.PathEscape(branchID) + "/roles/" + url.PathEscape(roleName)
	var existing roleResponse
	err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &existing, http.StatusOK)
	if err == nil {
		if existing.Role.Name != roleName {
			return managedpostgres.ErrUnavailable
		}
		return nil
	}
	if !errors.Is(err, managedpostgres.ErrNotFound) {
		return err
	}
	var request createRoleRequest
	request.Role.Name = roleName
	var created roleResponse
	collectionPath := "/projects/" + url.PathEscape(projectID) + "/branches/" + url.PathEscape(branchID) + "/roles"
	err = p.doJSON(ctx, http.MethodPost, collectionPath, nil, request, &created, http.StatusCreated)
	if errors.Is(err, managedpostgres.ErrConflict) {
		// A previous request can have succeeded after the response path
		// failed. The deterministic role name makes the conflict a safe
		// recovery point; never reset the password on an idempotent retry.
		return nil
	}
	if err != nil {
		return err
	}
	if created.Role.Name != roleName {
		return managedpostgres.ErrUnavailable
	}
	return p.waitForOperations(ctx, projectID, created.Operations)
}

func (p *Provider) waitForOperations(ctx context.Context, projectID string, operations []operation) error {
	for _, initial := range operations {
		current := initial
		if current.ID == "" {
			return managedpostgres.ErrUnavailable
		}
		for !operationFinished(current.Status) {
			if current.Status == "error" || current.Status == "cancelled" {
				return managedpostgres.ErrUnavailable
			}
			timer := time.NewTimer(p.credentialPollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			path := "/projects/" + url.PathEscape(projectID) + "/operations/" + url.PathEscape(current.ID)
			var response struct {
				Operation operation `json:"operation"`
			}
			if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &response, http.StatusOK); err != nil {
				return err
			}
			current = response.Operation
		}
	}
	return nil
}

func operationFinished(status string) bool {
	return status == "finished" || status == "skipped"
}

func (p *Provider) credentialMaterial(ctx context.Context, projectID, branchID, roleName string) (managedpostgres.CredentialMaterial, error) {
	var directResponse connectionURIResponse
	var pooledResponse connectionURIResponse
	group, groupContext := errgroup.WithContext(ctx)
	group.Go(func() error {
		return p.connectionURI(groupContext, projectID, branchID, roleName, false, &directResponse)
	})
	group.Go(func() error {
		return p.connectionURI(groupContext, projectID, branchID, roleName, true, &pooledResponse)
	})
	if err := group.Wait(); err != nil {
		return managedpostgres.CredentialMaterial{}, err
	}
	direct, err := parseConnectionURI(directResponse.URI)
	if err != nil {
		return managedpostgres.CredentialMaterial{}, err
	}
	pooled, err := parseConnectionURI(pooledResponse.URI)
	if err != nil || direct.username != pooled.username || direct.password != pooled.password || direct.database != pooled.database || direct.tlsMode != pooled.tlsMode {
		return managedpostgres.CredentialMaterial{}, managedpostgres.ErrUnavailable
	}
	material := managedpostgres.CredentialMaterial{
		ProviderIdentityID: roleName,
		Username:           direct.username,
		Password:           direct.password,
		Database:           direct.database,
		TLSMode:            direct.tlsMode,
		Endpoints: []managedpostgres.Endpoint{
			{Role: managedpostgres.EndpointPooled, Host: pooled.host, Port: pooled.port},
			{Role: managedpostgres.EndpointDirect, Host: direct.host, Port: direct.port},
		},
	}
	if err := material.Validate(); err != nil {
		return managedpostgres.CredentialMaterial{}, managedpostgres.ErrUnavailable
	}
	return material, nil
}

func (p *Provider) connectionURI(ctx context.Context, projectID, branchID, roleName string, pooled bool, response *connectionURIResponse) error {
	query := url.Values{
		"branch_id":     {branchID},
		"database_name": {p.databaseName},
		"role_name":     {roleName},
		"pooled":        {strconv.FormatBool(pooled)},
	}
	path := "/projects/" + url.PathEscape(projectID) + "/connection_uri"
	return p.doJSON(ctx, http.MethodGet, path, query, nil, response, http.StatusOK)
}

func parseConnectionURI(value string) (parsedConnection, error) {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.User == nil || parsed.Hostname() == "" || parsed.RawQuery == "" || parsed.Fragment != "" {
		return parsedConnection{}, managedpostgres.ErrUnavailable
	}
	password, ok := parsed.User.Password()
	if !ok || password == "" {
		return parsedConnection{}, managedpostgres.ErrUnavailable
	}
	port := uint64(5432)
	if parsed.Port() != "" {
		port, err = strconv.ParseUint(parsed.Port(), 10, 16)
		if err != nil || port == 0 {
			return parsedConnection{}, managedpostgres.ErrUnavailable
		}
	}
	database, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || database == "" || strings.Contains(database, "/") {
		return parsedConnection{}, managedpostgres.ErrUnavailable
	}
	tlsMode := parsed.Query().Get("sslmode")
	if tlsMode != "require" && tlsMode != "verify-ca" && tlsMode != "verify-full" {
		return parsedConnection{}, managedpostgres.ErrUnavailable
	}
	return parsedConnection{
		username: parsed.User.Username(), password: password, database: database,
		tlsMode: tlsMode, host: parsed.Hostname(), port: uint16(port),
	}, nil
}

func (p *Provider) roleName(projectID, identityKey string) string {
	sum := sha256.Sum256([]byte(p.organizationID + "\x00" + projectID + "\x00" + identityKey))
	return "gregale_" + hex.EncodeToString(sum[:20])
}
