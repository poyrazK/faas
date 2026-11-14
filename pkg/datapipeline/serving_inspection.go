package datapipeline

import (
	"context"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func tableProjectionSupported(t api.DataSourceTable) bool {
	if t.Supported {
		return true
	}
	key := false
	for _, c := range t.Columns {
		if c.PrimaryKey {
			if c.Unsupported {
				return false
			}
			key = true
		}
	}
	if !key {
		return false
	}
	for _, issue := range t.Issues {
		if issue.Column == "" {
			return false
		}
	}
	return true
}

func selectedColumns(s *api.DataPipelineServing, table string) []string {
	if s == nil || len(s.Columns) == 0 {
		return nil
	}
	return s.Columns[table]
}

func projectTable(table api.DataSourceTable, s *api.DataPipelineServing) (api.DataSourceTable, error) {
	if s == nil {
		return table, nil
	}
	columns := selectedColumns(s, table.Name)
	if len(s.Columns) > 0 && len(columns) == 0 {
		return table, ErrInvalid
	}
	if len(columns) > 0 {
		present := map[string]bool{}
		projected := []api.DataSourceColumn{}
		for _, c := range table.Columns {
			included := slices.Contains(columns, c.Name)
			if c.PrimaryKey && !included {
				return table, ErrInvalid
			}
			if included {
				projected = append(projected, c)
				present[c.Name] = true
			}
		}
		if len(present) != len(columns) {
			return table, ErrInvalid
		}
		issues := []api.DataConnectionIssue{}
		for _, issue := range table.Issues {
			if issue.Column == "" || present[issue.Column] {
				issues = append(issues, issue)
			}
		}
		table.Columns, table.Issues, table.Supported = projected, issues, len(issues) == 0
	}
	if s.TenantAccess != nil {
		found := false
		for _, c := range table.Columns {
			if c.Name == s.TenantAccess.Column {
				found = c.Type == "text" || c.Type == "uuid" || strings.HasPrefix(c.Type, "character varying")
			}
		}
		if !found {
			return table, ErrInvalid
		}
	}
	return table, nil
}

// ValidateServingSelection checks an explicit projection against the same fresh
// metadata used for admission, before a CLI/setup requests a billable mirror.
func ValidateServingSelection(check api.DataConnectionCheck, tables []string, serving *api.DataPipelineServing) error {
	if serving == nil {
		return checkSelection(check, tables)
	}
	check.Tables = slices.Clone(check.Tables)
	for i, table := range check.Tables {
		if !slices.Contains(tables, table.Name) {
			continue
		}
		projected, err := projectTable(table, serving)
		if err != nil {
			return err
		}
		check.Tables[i] = projected
	}
	return checkSelection(check, tables)
}

func inspectPipelineTable(ctx context.Context, p Pipeline, source sourceMetadata, name string) (relation, api.DataSourceTable, error) {
	r, table, err := inspectSourceTable(ctx, source, name)
	if err != nil || p.Serving == nil {
		return r, table, err
	}
	table, err = projectTable(table, p.Serving)
	if err != nil {
		return r, table, &blocked{"source_serving_contract_changed", p.Initialized}
	}
	if columns := selectedColumns(p.Serving, name); len(columns) > 0 {
		projected := make([]column, 0, len(columns))
		for _, c := range r.Columns {
			if slices.Contains(columns, c.Name) {
				projected = append(projected, c)
			}
		}
		r.Columns = projected
	}
	r.Unsupported = !table.Supported
	return r, table, nil
}
