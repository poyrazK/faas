package edgedns

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/libdns/libdns"
)

// DNS01 publishes edgednsd's ACME DNS-01 challenges in one zone
// (ADR-20261103163954 §3), with the token edgednsd already holds for its
// address records. It is the libdns provider certmagic's DNS-01 solver
// calls: TXT records only, created and deleted by Cloudflare record id.
type DNS01 struct {
	cf     *Cloudflare
	zoneID string
}

// DNS01 returns the challenge provider for the zone with id zoneID.
func (c *Cloudflare) DNS01(zoneID string) (*DNS01, error) {
	if strings.TrimSpace(zoneID) == "" {
		return nil, errors.New("edgedns: DNS-01 needs the zone id")
	}
	return &DNS01{cf: c, zoneID: zoneID}, nil
}

// AppendRecords creates each TXT record and returns it with its Cloudflare
// id as ProviderData, which DeleteRecords needs.
func (d *DNS01) AppendRecords(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	out := make([]libdns.Record, 0, len(recs))
	for _, rec := range recs {
		rr := rec.RR()
		if rr.Type != "TXT" {
			return out, fmt.Errorf("edgedns: DNS-01 writes TXT records only, not %s", rr.Type)
		}
		name := strings.TrimSuffix(libdns.AbsoluteName(rr.Name, zone), ".")
		body := map[string]any{"type": "TXT", "name": name, "content": rr.Data, "ttl": DNSOnlyTTL, "comment": RecordComment}
		var created cfRecord
		if err := d.cf.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(d.zoneID)+"/dns_records", body, &created); err != nil {
			return out, err
		}
		if created.ID == "" {
			return out, fmt.Errorf("edgedns: Cloudflare returned no id for the TXT record %s", name)
		}
		out = append(out, libdns.TXT{Name: rr.Name, TTL: rr.TTL, Text: rr.Data, ProviderData: created.ID})
	}
	return out, nil
}

// DeleteRecords removes the records AppendRecords created. A record without
// a Cloudflare id was not created here and is left alone.
func (d *DNS01) DeleteRecords(ctx context.Context, _ string, recs []libdns.Record) ([]libdns.Record, error) {
	out := make([]libdns.Record, 0, len(recs))
	for _, rec := range recs {
		txt, ok := rec.(libdns.TXT)
		if !ok {
			continue
		}
		id, _ := txt.ProviderData.(string)
		if id == "" {
			continue
		}
		if err := d.cf.do(ctx, http.MethodDelete, "/zones/"+url.PathEscape(d.zoneID)+"/dns_records/"+url.PathEscape(id), nil, nil); err != nil {
			return out, err
		}
		out = append(out, rec)
	}
	return out, nil
}
