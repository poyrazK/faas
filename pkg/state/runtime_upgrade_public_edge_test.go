package state

// adr: 699

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPgPublicEdgeMemberCanonicalBoundsAndCopy(t *testing.T) {
	members := make([]RuntimeUpgradePublicEdgeMember, api.RuntimeUpgradePublicEdgeLimit)
	for i := range members {
		members[i] = RuntimeUpgradePublicEdgeMember{SlotID: uuid.NewString(), SessionID: uuid.NewString(), ConfigSHA256: strings.Repeat("a", 64)}
	}
	out, err := canonicalPublicEdgeMembers("", uuid.NewString(), strings.Repeat("c", 64), members)
	if err != nil || len(out) != api.RuntimeUpgradePublicEdgeLimit {
		t.Fatal(out, err)
	}
	old := members[0].SlotID
	out[0].SlotID = uuid.NewString()
	if members[0].SlotID != old {
		t.Fatal("canonical review aliased input")
	}
	for _, edit := range []func([]RuntimeUpgradePublicEdgeMember){
		func(m []RuntimeUpgradePublicEdgeMember) {
			m[0].SlotID = strings.ToUpper("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
		},
		func(m []RuntimeUpgradePublicEdgeMember) { m[0].SessionID = uuid.Nil.String() },
		func(m []RuntimeUpgradePublicEdgeMember) { m[1].SessionID = m[0].SessionID },
		func(m []RuntimeUpgradePublicEdgeMember) { m[0].ConfigSHA256 = strings.Repeat("A", 64) },
	} {
		bad := append([]RuntimeUpgradePublicEdgeMember(nil), members...)
		edit(bad)
		if _, err := canonicalPublicEdgeMembers("", uuid.NewString(), strings.Repeat("c", 64), bad); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal("invalid canonical member accepted", err)
		}
	}
}
