package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// resolveTriggerRef turns the trigger slug `trigger list` shows into its ID.
// `trigger get transforms` sent the slug as the ID and the server answered
// "Bad trigger id: invalid UUID length: 10" (prod hunt r60). A UUID passes
// through unchanged; a slug must name exactly one of the account's triggers.
func resolveTriggerRef(ctx context.Context, client *api.Client, ref string) (string, error) {
	if _, err := uuid.Parse(ref); err == nil {
		return ref, nil
	}
	triggers, err := client.GetTriggers(ctx, "", "")
	if err != nil {
		return "", fmt.Errorf("look up trigger %q: %w", ref, err)
	}
	var ids []string
	for _, trigger := range triggers {
		if trigger.Slug == ref {
			ids = append(ids, trigger.ID)
		}
	}
	switch len(ids) {
	case 1:
		return ids[0], nil
	case 0:
		return "", fmt.Errorf("no trigger has slug or ID %q; `gregale trigger list` shows both", ref)
	}
	sort.Strings(ids)
	return "", errors.New("slug " + ref + " names " + fmt.Sprint(len(ids)) + " triggers (" + strings.Join(ids, ", ") + "); pass one of their IDs")
}
