package main

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Valid app slugs fit the database name limit with either form. A distinct
// suffix keeps digit-led slugs from colliding with existing NAME-mirror names.
func pipelineMirrorName(name string) string {
	if name[0] >= '0' && name[0] <= '9' {
		return "p-" + name + "-mirror-db"
	}
	return name + "-mirror"
}

func samePipelineSelection(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func pipelineID(id string) string {
	if parsed, err := uuid.Parse(id); err == nil {
		return parsed.String()
	}
	return id
}

// An accepted pipeline already owns its destination and capacity. Recover its
// exact intent before performing probes meant for an empty, unused mirror.
func recoverPipelineCreate(ctx context.Context, client *api.Client, request api.CreateDataPipelineRequest, database, region string) (api.DataPipeline, bool, error) {
	list, err := client.ListDataPipelines(ctx)
	if err != nil {
		return api.DataPipeline{}, false, err
	}
	for _, candidate := range list.Items {
		if candidate.Name != request.Name {
			continue
		}
		p, err := client.GetDataPipeline(ctx, candidate.ID)
		if err != nil {
			return api.DataPipeline{}, false, err
		}
		if p.Name != request.Name || p.DesiredState == "deleted" || p.State == "deleted" || pipelineID(p.SourceConnectionID) != pipelineID(request.SourceConnectionID) || !samePipelineSelection(p.Tables, request.Tables) || !samePipelineSelection(p.ReadSubjects, request.ReadSubjects) {
			return api.DataPipeline{}, false, errors.New("the pipeline name already belongs to a different or deleted intent; inspect pipeline status or use a new name")
		}
		if database != "" {
			if parsed, parseErr := uuid.Parse(database); parseErr == nil {
				if parsed.String() != pipelineID(p.DatabaseID) {
					return api.DataPipeline{}, false, errors.New("the existing pipeline uses a different mirror")
				}
			} else {
				db, err := resolveManagedPostgresDatabase(ctx, client, database)
				if err != nil {
					return api.DataPipeline{}, false, err
				}
				if pipelineID(db.ID) != pipelineID(p.DatabaseID) {
					return api.DataPipeline{}, false, errors.New("the existing pipeline uses a different mirror")
				}
			}
		}
		if region != "" {
			db, err := client.GetManagedPostgresDatabase(ctx, p.DatabaseID)
			if err != nil {
				return api.DataPipeline{}, false, err
			}
			if db.Region != region {
				return api.DataPipeline{}, false, errors.New("the existing mirror uses a different region")
			}
		}
		return p, true, nil
	}
	return api.DataPipeline{}, false, nil
}
