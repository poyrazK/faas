package netns

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

const PublicForwardTable = "faas_public_forward"

// These stateless host rules also live in the rendered host policy, so an
// atomic full-policy reload retains protection for existing marked sockets.
// Variable XOR requires current nftables/kernel support; any setup failure
// refuses native boot before a public bridge or guest can be published.
func PublicForwardPolicyCommands() [][]string {
	flag := fmt.Sprintf("%#x", api.PublicForwardMarkFlag)
	return [][]string{
		{"nft", "add", "table", "inet", PublicForwardTable},
		{"nft", "add", "chain", "inet", PublicForwardTable, "output", "{", "type", "filter", "hook", "output", "priority", "-1", ";", "policy", "accept", ";", "}"},
		{"nft", "flush", "chain", "inet", PublicForwardTable, "output"},
		{"nft", "add", "rule", "inet", PublicForwardTable, "output", "meta", "mark", "&", flag, "==", flag, "meta", "mark", "set", "meta", "mark", "^", "meta", "oif"},
		{"nft", "add", "rule", "inet", PublicForwardTable, "output", "meta", "mark", "&", flag, "==", flag, "meta", "mark", "!=", flag, "drop"},
		{"nft", "add", "rule", "inet", PublicForwardTable, "output", "meta", "mark", "==", flag, "meta", "mark", "set", "meta", "mark", "^", "meta", "oif"},
	}
}

func publicForwardPolicyRender() string {
	flag := fmt.Sprintf("%#x", api.PublicForwardMarkFlag)
	return fmt.Sprintf("\ntable inet %s {\n  chain output {\n    type filter hook output priority -1; policy accept;\n    meta mark & %s == %s meta mark set meta mark ^ meta oif\n    meta mark & %s == %s meta mark != %s drop\n    meta mark == %s meta mark set meta mark ^ meta oif\n  }\n}\n", PublicForwardTable, flag, flag, flag, flag, flag, flag)
}
