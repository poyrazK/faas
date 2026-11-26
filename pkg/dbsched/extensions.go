package dbsched

import (
	"context"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

// Extensions lists or enables extensions on the primary. An upgrade check
// also reads the configured target image on the same node.
func (s *Scheduler) Extensions(ctx context.Context, id, database, enable string, major int) (pgextension.Catalog, []pgextension.Installed, error) {
	unlock := s.lock(id)
	defer unlock()
	return s.extensionsLocked(ctx, id, database, enable, major)
}

func (s *Scheduler) extensionsLocked(ctx context.Context, id, database, enable string, major int) (pgextension.Catalog, []pgextension.Installed, error) {
	req := dbguest.Request{Op: dbguest.OpExtensions, DiagnosticDatabase: database, EnableExtension: enable}
	if req.Validate() != nil || major != 0 && (enable != "" || major < dbguest.MinMajor || major > dbguest.MaxMajor) {
		return pgextension.Catalog{}, nil, ErrInvalid
	}
	vm, err := s.wakeLocked(ctx, id)
	if err != nil {
		return pgextension.Catalog{}, nil, err
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return pgextension.Catalog{}, nil, err
	}
	resp, err := s.vmm.Control(ctx, node, id, req)
	if err != nil {
		return pgextension.Catalog{}, nil, err
	}
	if resp.Error == dbguest.ErrCodeExtension {
		return pgextension.Catalog{}, nil, fmt.Errorf("%w: extension is unavailable on the running PostgreSQL image", ErrInvalid)
	}
	if resp.Error != "" || resp.Extensions == nil || !resp.Extensions.Valid() {
		return pgextension.Catalog{}, nil, fmt.Errorf("%w: PostgreSQL extension catalog is unavailable", ErrNodeUnavailable)
	}
	if major == 0 {
		return *resp.Extensions, nil, nil
	}
	if major <= vm.Major || !node.offers(major) {
		return pgextension.Catalog{}, nil, fmt.Errorf("%w: node does not offer that later PostgreSQL major", ErrInvalid)
	}
	if len(resp.Extensions.Installed) == 0 {
		return *resp.Extensions, nil, nil
	}
	reader, ok := s.vmm.(interface {
		extensionFiles(context.Context, Node, string, int) ([]string, error)
	})
	if !ok {
		return pgextension.Catalog{}, nil, ErrNodeUnavailable
	}
	files, err := reader.extensionFiles(ctx, node, id, major)
	if err != nil || len(files) == 0 || len(files) > 4096 {
		return pgextension.Catalog{}, nil, fmt.Errorf("%w: target PostgreSQL extension inventory is unavailable", ErrNodeUnavailable)
	}
	return *resp.Extensions, pgextension.UpgradeBlockers(resp.Extensions.Installed, files), nil
}

func (g *GRPCVMM) extensionFiles(ctx context.Context, node Node, id string, major int) ([]string, error) {
	c, err := g.client(ctx, node)
	if err != nil {
		return nil, err
	}
	resp, err := c.DatabaseControl(ctx, &vmmdpb.DatabaseControlRequest{
		DatabaseId: id, Op: "base_extensions", ExtensionsBaseMajor: int32(major), //nolint:gosec // validated before use
	})
	if err != nil {
		return nil, err
	}
	return resp.GetExtensionFiles(), nil
}
