// Package domainhosts implements a wildcard custom domain's optional host
// allowlist (ADR-520 amendment 2). The edge issues on-demand certificates for
// any name below a verified wildcard, so anyone sending random server names
// can spend the wildcard's new-host budget. With an allowlist only the listed
// names get certificates; routing is unchanged.
package domainhosts

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Read returns d's allowlist. d is a domain the caller may read.
func Read(ctx context.Context, store any, d state.CustomDomain) (api.CustomDomainHostsResponse, *api.Problem) {
	hosts, problem := allowlistStore(store, d)
	if problem != nil {
		return api.CustomDomainHostsResponse{}, problem
	}
	names, err := hosts.CustomDomainTLSAllowedHosts(ctx, d.Domain)
	if err != nil {
		return api.CustomDomainHostsResponse{}, storeProblem(err, "could not read the host allowlist")
	}
	return response(d.Domain, names), nil
}

// Replace sets d's allowlist to req.Names. d is a domain the caller may
// configure.
func Replace(ctx context.Context, store any, d state.CustomDomain, req api.SetCustomDomainHostsRequest) (api.CustomDomainHostsResponse, *api.Problem) {
	hosts, problem := allowlistStore(store, d)
	if problem != nil {
		return api.CustomDomainHostsResponse{}, problem
	}
	if len(req.Names) == 0 {
		return api.CustomDomainHostsResponse{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad request", "names must list at least one host; DELETE the allowlist to allow every name")
	}
	if len(req.Names) > api.CustomDomainTLSAllowedHostsMax {
		return api.CustomDomainHostsResponse{}, api.ErrCustomDomainHostsTooMany(len(req.Names))
	}
	names, err := state.NormalizeWildcardHostNames(d.Domain, req.Names)
	if err != nil {
		return api.CustomDomainHostsResponse{}, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid host name", err.Error())
	}
	if err := hosts.SetCustomDomainTLSAllowedHosts(ctx, d.Domain, names); err != nil {
		return api.CustomDomainHostsResponse{}, storeProblem(err, "could not save the host allowlist")
	}
	return response(d.Domain, names), nil
}

// Clear removes d's allowlist. d is a domain the caller may configure.
func Clear(ctx context.Context, store any, d state.CustomDomain) *api.Problem {
	hosts, problem := allowlistStore(store, d)
	if problem != nil {
		return problem
	}
	if err := hosts.SetCustomDomainTLSAllowedHosts(ctx, d.Domain, nil); err != nil {
		return storeProblem(err, "could not clear the host allowlist")
	}
	return nil
}

func allowlistStore(store any, d state.CustomDomain) (state.CustomDomainTLSAllowedHostStore, *api.Problem) {
	if !state.IsWildcardCustomDomain(d.Domain) {
		return nil, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Not a wildcard domain", "only a wildcard custom domain (*.example.com) has a host allowlist").
			WithDocs("https://gregale.dev/docs/domains#wildcard-host-allowlist")
	}
	hosts, ok := store.(state.CustomDomainTLSAllowedHostStore)
	if !ok {
		return nil, api.ErrCapacity("host allowlist storage unavailable")
	}
	return hosts, nil
}

func storeProblem(err error, detail string) *api.Problem {
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such domain")
	}
	return api.ErrCapacity(detail)
}

func response(domain string, names []string) api.CustomDomainHostsResponse {
	if names == nil {
		names = []string{}
	}
	return api.CustomDomainHostsResponse{Domain: domain, Names: names, Restricted: len(names) > 0}
}
