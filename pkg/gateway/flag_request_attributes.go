package gateway

// Gateway-verified request attributes for Flags targeting
// (ADR 20261114150000 §2). The handler resolves them once per public
// request; every guest identity stamped for that request, including retries
// onto a sibling, carries the same values.

import (
	"context"
	"net/http"
)

type flagRequestAttributesKey struct{}

type flagRequestAttributes struct {
	country  string
	uaFamily string
}

// withFlagRequestAttributes resolves the client's country and browser family
// with the same trusted client-IP chain, geo database and family set as
// request analytics. Unknown values are left empty, so the guest sees no
// header rather than a placeholder a rule could match.
func (h *Handler) withFlagRequestAttributes(r *http.Request) *http.Request {
	attrs := flagRequestAttributes{
		country:  h.lookupTelemetryCountry(r),
		uaFamily: normalizeTelemetryUserAgent(r.Header.Get("User-Agent")),
	}
	if attrs.country == telemetryUnknownDimension {
		attrs.country = ""
	}
	if attrs.uaFamily == telemetryUnknownDimension {
		attrs.uaFamily = ""
	}
	return r.WithContext(context.WithValue(r.Context(), flagRequestAttributesKey{}, attrs))
}

func flagRequestAttributesFrom(ctx context.Context) flagRequestAttributes {
	attrs, _ := ctx.Value(flagRequestAttributesKey{}).(flagRequestAttributes)
	return attrs
}
