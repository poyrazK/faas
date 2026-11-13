// adr: 20261113160716 — tenant-scoped pipeline serving and column projection.
package datapipeline

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/connectioncapacity"
)

// NormalizeCreateRequest shares immutable intent canonicalization with clients.
func NormalizeCreateRequest(r api.CreateDataPipelineRequest) api.CreateDataPipelineRequest {
	return normalize(r)
}

func normalizeServing(in *api.DataPipelineServing) *api.DataPipelineServing {
	if in == nil {
		return nil
	}
	b, _ := json.Marshal(in)
	var out api.DataPipelineServing
	_ = json.Unmarshal(b, &out)
	if out.Version == 0 {
		out.Version = 1
	}
	for _, columns := range out.Columns {
		slices.Sort(columns)
	}
	if out.TenantAccess != nil {
		out.TenantAccess.Memberships = append([]api.DataPipelineTenantMembership{}, out.TenantAccess.Memberships...)
		slices.SortFunc(out.TenantAccess.Memberships, compareMemberships)
	}
	return &out
}

func compareMemberships(a, b api.DataPipelineTenantMembership) int {
	if c := strings.Compare(a.Subject, b.Subject); c != 0 {
		return c
	}
	return strings.Compare(a.Tenant, b.Tenant)
}

func validMemberships(members []api.DataPipelineTenantMembership) bool {
	if len(members) > api.DataPipelineTenantMembershipsMax {
		return false
	}
	seen := map[api.DataPipelineTenantMembership]bool{}
	for _, m := range members {
		if !validReaders([]string{m.Subject}) || m.Tenant == "" || len(m.Tenant) > api.DataPipelineTenantMaxBytes || !utf8.ValidString(m.Tenant) || strings.ContainsAny(m.Tenant, "\x00\r\n") || seen[m] {
			return false
		}
		seen[m] = true
	}
	return len(membershipSubjects(members)) <= api.DataPipelineSubjectsMax
}

func membershipSubjects(members []api.DataPipelineTenantMembership) []string {
	set := map[string]bool{}
	for _, m := range members {
		set[m.Subject] = true
	}
	out := make([]string, 0, len(set))
	for subject := range set {
		out = append(out, subject)
	}
	slices.Sort(out)
	return out
}

func validServing(s *api.DataPipelineServing, tables []string) bool {
	if s == nil {
		return true
	}
	if s.Version != 1 || len(s.Columns) == 0 && s.TenantAccess == nil {
		return false
	}
	if len(s.Columns) > 0 {
		if len(s.Columns) != len(tables) {
			return false
		}
		for _, table := range tables {
			columns := s.Columns[table]
			if len(columns) == 0 || len(columns) > api.DataPipelineColumnsMax {
				return false
			}
			seen := map[string]bool{}
			for _, column := range columns {
				if !validServingColumn(column) || seen[column] {
					return false
				}
				seen[column] = true
			}
			if s.TenantAccess != nil && !seen[s.TenantAccess.Column] {
				return false
			}
		}
	}
	return s.TenantAccess == nil || validServingColumn(s.TenantAccess.Column) && validMemberships(s.TenantAccess.Memberships)
}

func sameServing(a, b *api.DataPipelineServing) bool {
	return reflect.DeepEqual(normalizeServing(a), normalizeServing(b))
}

func servingJSON(s *api.DataPipelineServing) []byte {
	if s == nil {
		return []byte("{}")
	}
	b, _ := json.Marshal(s)
	return b
}

func tenantAccess(p Pipeline) *api.DataPipelineTenantAccess {
	if p.Serving == nil {
		return nil
	}
	return p.Serving.TenantAccess
}

func validRuntimeServing(p Pipeline) bool {
	if p.Serving != nil && p.Serving.Version != 1 {
		return false
	}
	s := normalizeServing(p.Serving)
	if s != nil && len(s.Columns) > 0 {
		active := map[string][]string{}
		for _, table := range p.Tables {
			active[table] = s.Columns[table]
		}
		s.Columns = active
	}
	return validServing(s, p.Tables) && (tenantAccess(p) == nil || slices.Equal(membershipSubjects(tenantAccess(p).Memberships), p.ReadSubjects))
}

func membershipsSubset(before, after []api.DataPipelineTenantMembership) bool {
	for _, m := range after {
		if !slices.Contains(before, m) {
			return false
		}
	}
	return true
}

func setupCreationSubjects(r SetupRequest) []string {
	if r.Serving != nil && r.Serving.TenantAccess != nil {
		return nil
	}
	return r.ReadSubjects
}

func sourceServingSetupCheck(c api.DataConnectionCheck, tables []string, serving *api.DataPipelineServing) error {
	if err := ValidateServingSelection(c, tables, serving); err != nil {
		return err
	}
	if c.Capacity == nil || c.Capacity.Source == nil || c.Capacity.Source.State != "ready" {
		if c.Capacity != nil && c.Capacity.Source != nil && c.Capacity.Source.State == "insufficient_capacity" {
			return connectioncapacity.ErrInsufficient
		}
		return connectioncapacity.ErrUnknown
	}
	return nil
}

func validServingColumn(name string) bool {
	return name != "" && len(name) <= api.DataPipelineIdentifierMaxBytes && utf8.ValidString(name) && !strings.ContainsRune(name, 0)
}
