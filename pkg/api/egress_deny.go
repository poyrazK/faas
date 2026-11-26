package api

// TenantEgressDenyCIDRs are the ranges every tenant netns drops (spec §11),
// as pkg/netns.NewDefaultDenySet installs them. apid refuses an egress
// allowlist entry inside one, because the netns denylist would drop its
// traffic anyway (prod hunt r58). apid may not import pkg/netns (depguard:
// host networking is vmmd's), so the list lives here and a netns test pins
// the two together.
var TenantEgressDenyCIDRs = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10",
	"fe80::/10", "fc00::/7", "ff00::/8", "::1/128", "::/128", "2002::/16", "2001::/32", "64:ff9b::/96", "64:ff9b:1::/48",
}
