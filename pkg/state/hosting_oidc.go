package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

type HostingOIDCStore interface {
	CreateHostingOIDCTrust(context.Context, api.HostingOIDCTrust) (api.HostingOIDCTrust, error)
	ListHostingOIDCTrusts(context.Context, string, string) ([]api.HostingOIDCTrust, error)
	GetHostingOIDCTrust(context.Context, string, string) (api.HostingOIDCTrust, error)
	RevokeHostingOIDCTrust(context.Context, string, string, string) error
	CreateHostingOIDCCredential(context.Context, api.HostingOIDCTrust, api.HostingAutomationCredential) (api.HostingAutomationCredential, error)
}
