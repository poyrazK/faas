// adr: 20261102081618
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type recordingPlacement struct {
	node, nodes string
}

func (p *recordingPlacement) Prefer(*http.Request) (PreferredNode, bool) {
	return PreferredNode{}, false
}
func (p *recordingPlacement) Learn(_ *http.Request, node string)     { p.node = node }
func (p *recordingPlacement) LearnSet(_ *http.Request, nodes string) { p.nodes = nodes }

func TestTakeInstanceNodeHandsTheNodeSetToAnEdgeThatTakesIt(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, tc := range []struct {
		name              string
		header            http.Header
		wantNode, wantSet string
	}{
		{name: "node set", header: http.Header{InstanceNodeHeader: {"node-a"}, InstanceNodesHeader: {"node-a=2,node-b=1"}}, wantSet: "node-a=2,node-b=1"},
		{name: "one node", header: http.Header{InstanceNodeHeader: {"node-a"}}, wantNode: "node-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			placement := &recordingPlacement{}
			p := (&InternalReverseProxy{}).WithEdgePlacement(placement)
			p.takeInstanceNode(req, tc.header)
			if placement.node != tc.wantNode || placement.nodes != tc.wantSet {
				t.Fatalf("learned node %q set %q, want %q %q", placement.node, placement.nodes, tc.wantNode, tc.wantSet)
			}
			if len(tc.header) != 0 {
				t.Fatalf("headers left for the client: %v", tc.header)
			}
		})
	}
}
