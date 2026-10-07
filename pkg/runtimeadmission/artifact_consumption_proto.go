package runtimeadmission

import (
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

func (c ArtifactConsumption) ToProto() *vmmdpb.RuntimeArtifactConsumption {
	if c.IsZero() {
		return nil
	}
	p := &vmmdpb.RuntimeArtifactConsumption{ConfigHash: c.ConfigHash, ProcessPid: c.ProcessPID, ProcessStart: c.ProcessStart}
	for _, d := range c.Drives {
		p.Drives = append(p.Drives, &vmmdpb.RuntimeConsumedDrive{Source: d.Source.ToProto(), DriveId: d.DriveID, ReadOnly: d.ReadOnly, RootDevice: d.RootDevice, ProducerDigest: d.ProducerDigest, ProducerBytes: d.ProducerBytes, InjectedDigest: d.InjectedDigest, InjectedBytes: d.InjectedBytes})
	}
	return p
}

func artifactConsumptionFromProto(p *vmmdpb.RuntimeArtifactConsumption) (ArtifactConsumption, error) {
	if p == nil {
		return ArtifactConsumption{}, nil
	}
	if RejectUnknown(p) != nil || len(p.Drives) > api.SidecarCapMax+2 {
		return ArtifactConsumption{}, ErrInvalid
	}
	c := ArtifactConsumption{ConfigHash: p.ConfigHash, ProcessPID: p.ProcessPid, ProcessStart: p.ProcessStart}
	for _, d := range p.Drives {
		if d == nil {
			return ArtifactConsumption{}, ErrInvalid
		}
		sources, err := ArtifactSourcesFromProto([]*vmmdpb.RuntimeArtifactSource{d.Source})
		if err != nil {
			return ArtifactConsumption{}, err
		}
		c.Drives = append(c.Drives, ConsumedDrive{Source: sources[0], DriveID: d.DriveId, ReadOnly: d.ReadOnly, RootDevice: d.RootDevice, ProducerDigest: d.ProducerDigest, ProducerBytes: d.ProducerBytes, InjectedDigest: d.InjectedDigest, InjectedBytes: d.InjectedBytes})
	}
	return c, nil
}
