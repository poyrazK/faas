package sched

// adr: 20261017 vmmd restart adoption

import (
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func TestAdoptionReportFromProtoRefusesAmbiguousInventories(t *testing.T) {
	item := func(id string) *vmmdpb.AdoptionReportInstance {
		return &vmmdpb.AdoptionReportInstance{Instance: id, AppId: "app"}
	}
	base := func() *vmmdpb.AdoptionReportResponse {
		return &vmmdpb.AdoptionReportResponse{NodeId: "node", Epoch: 2, Incarnation: "inc", Complete: true,
			Adopted: []*vmmdpb.AdoptionReportInstance{item("kept"), {Instance: "thawed", AppId: "app", Resolved: "thawed"}}, Refused: []*vmmdpb.AdoptionReportInstance{item("lost")},
			LeasedSinceStart: []string{"new", "lost"}}
	}
	got, err := adoptionReportFromProto(base())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Holds("kept") || !got.Holds("new") || !got.Holds("lost") || got.Holds("gone") {
		t.Fatalf("holds: %+v", got)
	}
	if got.Refused["lost"].AppID != "app" || got.Adopted["kept"] != "app" || got.Resolved["thawed"] != AdoptionResolvedThawed || got.Resolved["kept"] != "" {
		t.Fatalf("report = %+v", got)
	}
	for name, mutate := range map[string]func(*vmmdpb.AdoptionReportResponse){
		"no node":           func(r *vmmdpb.AdoptionReportResponse) { r.NodeId = "" },
		"no epoch":          func(r *vmmdpb.AdoptionReportResponse) { r.Epoch = 0 },
		"no incarnation":    func(r *vmmdpb.AdoptionReportResponse) { r.Incarnation = "" },
		"empty instance":    func(r *vmmdpb.AdoptionReportResponse) { r.Adopted = append(r.Adopted, item("")) },
		"adopted twice":     func(r *vmmdpb.AdoptionReportResponse) { r.Adopted = append(r.Adopted, item("kept")) },
		"adopted & refused": func(r *vmmdpb.AdoptionReportResponse) { r.Refused = append(r.Refused, item("kept")) },
		"empty lease":       func(r *vmmdpb.AdoptionReportResponse) { r.LeasedSinceStart = append(r.LeasedSinceStart, "") },
		"leased adopted":    func(r *vmmdpb.AdoptionReportResponse) { r.LeasedSinceStart = append(r.LeasedSinceStart, "kept") },
	} {
		r := base()
		mutate(r)
		if _, err := adoptionReportFromProto(r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
