package manifest

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ControlPlaneCell is the immutable database identity for one regional fleet.
type ControlPlaneCell struct {
	ID     string `yaml:"id"`
	Region string `yaml:"region"`
}

func (m *Manifest) validateControlPlaneCell() Errors {
	c := m.ControlPlaneCell
	if c == nil {
		return nil
	}
	var errs Errors
	id, err := uuid.Parse(c.ID)
	if err != nil || id == uuid.Nil || id.String() != c.ID {
		errs = append(errs, Error{"control_plane_cell.id", "must be a canonical nonzero UUID"})
	}
	if c.Region == "" || !api.ValidComputeRegion(c.Region) {
		errs = append(errs, Error{"control_plane_cell.region", "must be a canonical compute region"})
	}
	for i, host := range m.Fleet.Hosts {
		if host.Region != c.Region || host.Region == "" {
			errs = append(errs, Error{fmt.Sprintf("fleet.hosts[%d].region", i), "must match control_plane_cell.region"})
		}
	}
	return errs
}
