package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// ADR-20261109100000: a cloud manifest (no ssh section, no private card)
// renders the same boundary as before; a bare-metal one names its private
// card and restricts sshd to the overlay and the operator's sources.
func TestRenderManifestNetworkPolicyVars(t *testing.T) {
	cloud := renderManifestNetworkPolicyVars(manifest.Overlay{Provider: "static", CIDR: "10.10.0.0/20"}, nil)
	for _, want := range []string{`faas_private_iface: ""`, "faas_ssh_restricted: false"} {
		if !strings.Contains(cloud, want) {
			t.Errorf("cloud vars missing %q:\n%s", want, cloud)
		}
	}
	if strings.Contains(cloud, "faas_ssh_allowed_cidrs") {
		t.Errorf("cloud vars name sshd sources:\n%s", cloud)
	}

	ovh := renderManifestNetworkPolicyVars(
		manifest.Overlay{Provider: "static", CIDR: "10.20.0.0/24", Interface: "eno2"},
		&manifest.SSH{AllowedCIDRs: []string{"198.51.100.10/32"}},
	)
	for _, want := range []string{
		`faas_private_iface: "eno2"`,
		"faas_ssh_restricted: true",
		`faas_ssh_private_cidrs: ["10.20.0.0/24"]`,
		`faas_ssh_allowed_cidrs: ["198.51.100.10/32"]`,
	} {
		if !strings.Contains(ovh, want) {
			t.Errorf("bare-metal vars missing %q:\n%s", want, ovh)
		}
	}
	privateOnly := renderManifestNetworkPolicyVars(manifest.Overlay{Provider: "static", CIDR: "10.20.0.0/24"}, &manifest.SSH{})
	if !strings.Contains(privateOnly, "faas_ssh_allowed_cidrs: []") || !strings.Contains(privateOnly, "faas_ssh_restricted: true") {
		t.Errorf("an empty ssh section must restrict sshd to the private network:\n%s", privateOnly)
	}
}

// The static provider's private card replaces the default-route interface,
// which on bare metal is the public card.
func TestRenderManifestHostVarsUsesTheDeclaredPrivateCard(t *testing.T) {
	host := manifest.Host{Name: "ovh-cp-1", Role: roleControlPlane, Address: "10.20.0.1:50051"}
	render := func(overlay manifest.Overlay) string {
		return renderManifestHostVars(host, "10.20.0.1", "", "", "", "", nil, "", overlay, nil, "manual", "", "", "", "", nil, nil, nil, "")
	}
	if got := render(manifest.Overlay{Provider: "static", CIDR: "10.20.0.0/24", Interface: "eno2"}); !strings.Contains(got, `faas_overlay_iface: "eno2"`) || strings.Contains(got, "ansible_default_ipv4") {
		t.Errorf("declared private card not used:\n%s", got)
	}
	if got := render(manifest.Overlay{Provider: "static", CIDR: "10.20.0.0/24"}); !strings.Contains(got, "ansible_default_ipv4.interface") {
		t.Errorf("without a declared card the default-route interface must stay:\n%s", got)
	}
}
