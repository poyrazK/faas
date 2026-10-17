package runtimeadmission

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func consumedReceiptFixture(t *testing.T) Receipt {
	t.Helper()
	b := validBinding(time.Now())
	b.ProtocolVersion = ArtifactProtocolVersion
	sources := []ArtifactSource{
		{Kind: "base-image", StorageKey: "base/approved.ext4", Digest: "sha256:" + strings.Repeat("a", 64), Bytes: 4096},
		{Kind: "app-layer", StorageKey: "app/approved.ext4", Digest: "sha256:" + strings.Repeat("b", 64), Bytes: 8192},
		{Kind: "sidecar-layer", WorkloadName: "metrics", StorageKey: "sidecars/metrics.ext4", Digest: "sha256:" + strings.Repeat("c", 64), Bytes: 4096},
	}
	var err error
	b.ArtifactSourcesHash, err = HashArtifactSources(sources)
	if err != nil {
		t.Fatal(err)
	}
	c := ArtifactConsumption{ConfigHash: strings.Repeat("d", 64), ProcessPID: 42, ProcessStart: "101"}
	for _, source := range sources {
		c.Drives = append(c.Drives, ConsumedDrive{Source: source, DriveID: source.Role(), ReadOnly: source.Role() != "main", RootDevice: source.Role() == "base", ProducerDigest: source.Digest, ProducerBytes: source.Bytes, InjectedDigest: source.Digest, InjectedBytes: source.Bytes})
	}
	c.Drives[1].InjectedDigest = "sha256:" + strings.Repeat("e", 64)
	return Receipt{Binding: b, NativeInputHash: strings.Repeat("f", 64), Netns: "fc-consumed", HostIP: "10.100.0.2", LeaseUID: 20000, Method: vmmdpb.WakeMethod_WAKE_COLD_BOOT, CompletedAtUnixNano: time.Now().UnixNano(), ArtifactConsumption: c}
}

func TestArtifactSourceHashBindsExactSetAndIgnoresDeliveryOrder(t *testing.T) {
	r := consumedReceiptFixture(t)
	sources := make([]ArtifactSource, 0, len(r.ArtifactConsumption.Drives))
	for _, drive := range r.ArtifactConsumption.Drives {
		sources = append(sources, drive.Source)
	}
	slices.Reverse(sources)
	got, err := HashArtifactSources(sources)
	if err != nil || got != r.Binding.ArtifactSourcesHash {
		t.Fatal("source delivery order changed source-set authority", err)
	}
	for _, edit := range []func([]ArtifactSource){
		func(s []ArtifactSource) { s[0].Bytes++ },
		func(s []ArtifactSource) { s[0].StorageKey = "another/key.ext4" },
		func(s []ArtifactSource) { s[0].Digest = "sha256:" + strings.Repeat("0", 64) },
		func(s []ArtifactSource) { s[0].WorkloadName = "other" },
	} {
		changed := slices.Clone(sources)
		edit(changed)
		if hash, err := HashArtifactSources(changed); err == nil && hash == got {
			t.Fatal("changed source inherited the original source-set hash")
		}
	}
	if _, err := HashArtifactSources(sources[:1]); !errors.Is(err, ErrInvalid) {
		t.Fatal("incomplete drive set accepted", err)
	}
	duplicate := slices.Clone(sources)
	duplicate[0] = duplicate[1]
	if _, err := HashArtifactSources(duplicate); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate role and key accepted", err)
	}
}

func TestArtifactReceiptRequiresCompleteNativeFacts(t *testing.T) {
	r := consumedReceiptFixture(t)
	if err := r.Check(r.Binding, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func(*Receipt)
	}{
		{"missing consumption", func(r *Receipt) { r.ArtifactConsumption = ArtifactConsumption{} }},
		{"wrong config", func(r *Receipt) { r.ArtifactConsumption.ConfigHash = "invalid" }},
		{"no process", func(r *Receipt) { r.ArtifactConsumption.ProcessPID = 0 }},
		{"oversized pid", func(r *Receipt) { r.ArtifactConsumption.ProcessPID = ^uint32(0) }},
		{"no start", func(r *Receipt) { r.ArtifactConsumption.ProcessStart = "" }},
		{"noncanonical start", func(r *Receipt) { r.ArtifactConsumption.ProcessStart = "0101" }},
		{"missing drive", func(r *Receipt) { r.ArtifactConsumption.Drives = r.ArtifactConsumption.Drives[:2] }},
		{"duplicate drive", func(r *Receipt) { r.ArtifactConsumption.Drives[2].DriveID = r.ArtifactConsumption.Drives[0].DriveID }},
		{"unapproved source", func(r *Receipt) { r.ArtifactConsumption.Drives[1].Source.StorageKey = "unapproved.ext4" }},
		{"producer changed", func(r *Receipt) { r.ArtifactConsumption.Drives[1].ProducerDigest = "sha256:" + strings.Repeat("0", 64) }},
		{"producer size", func(r *Receipt) { r.ArtifactConsumption.Drives[1].ProducerBytes++ }},
		{"injected size", func(r *Receipt) { r.ArtifactConsumption.Drives[1].InjectedBytes++ }},
		{"invalid injected digest", func(r *Receipt) { r.ArtifactConsumption.Drives[1].InjectedDigest = "invalid" }},
		{"mutated base", func(r *Receipt) {
			r.ArtifactConsumption.Drives[0].InjectedDigest = r.ArtifactConsumption.Drives[1].InjectedDigest
		}},
		{"writable sidecar", func(r *Receipt) { r.ArtifactConsumption.Drives[2].ReadOnly = false }},
		{"main root", func(r *Receipt) { r.ArtifactConsumption.Drives[1].RootDevice = true }},
		{"snapshot without lineage", func(r *Receipt) { r.Method = vmmdpb.WakeMethod_WAKE_RESTORE }},
		{"paused without lineage", func(r *Receipt) { r.Paused = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := r.Clone()
			test.edit(&changed)
			if changed.Check(r.Binding, time.Now()) == nil {
				t.Fatal("invalid native artifact receipt accepted")
			}
		})
	}
}

func TestArtifactReceiptRoundTripsWithoutAliasingOrVersionOneUpgrade(t *testing.T) {
	r := consumedReceiptFixture(t)
	p := r.ToProto()
	decoded, err := ReceiptFromProto(p)
	if err != nil || !decoded.Equal(r) || decoded.Check(r.Binding, time.Now()) != nil {
		t.Fatal("lossy consumed receipt wire round trip", err)
	}
	p.ArtifactConsumption.Drives[0].Source.StorageKey = "changed.ext4"
	if decoded.ArtifactConsumption.Drives[0].Source.StorageKey != r.ArtifactConsumption.Drives[0].Source.StorageKey {
		t.Fatal("wire object retained authority over decoded receipt")
	}
	encoded, err := json.Marshal(r)
	if err != nil || json.Unmarshal(encoded, &decoded) != nil || !decoded.Equal(r) {
		t.Fatal("lossy consumed receipt durable round trip", err)
	}
	old := r.Clone()
	old.Binding.ProtocolVersion, old.Binding.ArtifactSourcesHash = ProtocolVersion, ""
	if old.Check(old.Binding, time.Now()) == nil {
		t.Fatal("protocol 1 was upgraded by adding artifact fields")
	}
	old.ArtifactConsumption = ArtifactConsumption{}
	if old.Check(old.Binding, time.Now()) != nil {
		t.Fatal("legacy receipt interpretation changed")
	}
	encoded, err = json.Marshal(old)
	if err != nil || strings.Contains(string(encoded), "artifact_consumption") || strings.Contains(string(encoded), "artifact_sources_hash") {
		t.Fatal("legacy durable receipt shape changed", err)
	}
	copy := r.Clone()
	copy.ArtifactConsumption.Drives[0].DriveID = "changed"
	if copy.Equal(r) || r.ArtifactConsumption.Drives[0].DriveID == "changed" {
		t.Fatal("receipt clone aliased native drive facts")
	}
}
