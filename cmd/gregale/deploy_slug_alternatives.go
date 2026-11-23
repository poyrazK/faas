package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// appSlugAlternativeCount is how many suffixed names a deploy tries after
// the requested one turns out to belong to another owner.
const appSlugAlternativeCount = 3

// appSlugAlternatives prepares a deploy of an app the caller does not hold
// yet. owned is the caller's existing suffixed app for slug, when an earlier
// deploy already took one (a fresh checkout has no link to it); otherwise
// alternatives are the names to try, in order, if creating slug finds
// another owner holding it. Both are empty when the caller already holds
// slug, gregale.yaml names the app (its triggers, routes and bindings would
// no longer match a different name), or the owner cannot be read: the
// deploy then behaves as before and a conflict stays an error.
func appSlugAlternatives(ctx context.Context, client *Client, slug, sourceDir string) (owned string, alternatives []string) {
	if _, err := client.GetApp(ctx, slug); !isNotFound(err) {
		return "", nil
	}
	if manifestNamesApp(sourceDir, slug) {
		return "", nil
	}
	acct, err := client.Whoami(ctx)
	if err != nil || acct.ID == "" {
		return "", nil
	}
	// An organization key creates the app in the organization, so every
	// member deploying the same checkout gets the same alternative.
	owner := acct.ID
	if acct.KeyOrg != nil && acct.KeyOrg.ID != "" {
		owner = "org:" + acct.KeyOrg.ID
	}
	alternatives = api.SuffixedAppSlugs(owner, slug, appSlugAlternativeCount)
	for _, alt := range alternatives {
		if _, err := client.GetApp(ctx, alt); err == nil {
			return alt, alternatives
		}
	}
	return "", alternatives
}

// createOrFetchAppWithAlternatives is createOrFetchAppWith that, when another
// owner holds req.Slug, creates the app under the first free alternative
// instead of failing. It returns the slug the app was created or found under.
func createOrFetchAppWithAlternatives(ctx context.Context, client *Client, req, existingReq api.CreateAppRequest, alternatives []string, requireAuthnPtr *bool, appProtocolPtr *string, publicAuthPtr *api.PublicAuthBlock, resolved *api.AppResponse) (string, error) {
	err := createOrFetchAppWith(ctx, client, req, existingReq, requireAuthnPtr, appProtocolPtr, publicAuthPtr, resolved)
	var taken *slugTakenError
	if !errors.As(err, &taken) || len(alternatives) == 0 {
		return req.Slug, err
	}
	for _, alt := range alternatives {
		altReq, altExisting := req, existingReq
		altReq.Slug, altExisting.Slug = alt, alt
		err = createOrFetchAppWith(ctx, client, altReq, altExisting, requireAuthnPtr, appProtocolPtr, publicAuthPtr, resolved)
		if !errors.As(err, &taken) {
			return alt, err
		}
	}
	return req.Slug, fmt.Errorf("slug %q and its alternatives %s are already in use; pick a different --name", req.Slug, strings.Join(alternatives, ", "))
}

// printSlugAlternativeNotice tells the user their app has a different name
// from the one they asked for, and how to keep or change it.
func printSlugAlternativeNotice(requested, got string) {
	if jsonOutput || requested == got {
		return
	}
	_, _ = fmt.Fprintf(osStderr, "Note: the name %s belongs to another account, so this app is %s (%s). Pass --name to choose a different name.\n", requested, got, deployedAppURL(got))
}

// manifestNamesApp reports whether gregale.yaml in dir names slug as the app
// of any entry (a trigger, route, binding or function's app or to field).
func manifestNamesApp(dir, slug string) bool {
	if dir == "" {
		return false
	}
	m, ok, err := gregalemanifest.Load(dir)
	if err != nil {
		// An unreadable manifest fails the deploy elsewhere; do not rename
		// an app whose manifest we could not check.
		return true
	}
	if !ok || m == nil {
		return false
	}
	return valueNamesApp(reflect.ValueOf(m), slug, 0)
}

func valueNamesApp(v reflect.Value, slug string, depth int) bool {
	if depth > 32 {
		return false
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return !v.IsNil() && valueNamesApp(v.Elem(), slug, depth+1)
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			fv := v.Field(i)
			if (f.Name == "App" || f.Name == "To") && fv.Kind() == reflect.String && fv.String() == slug {
				return true
			}
			if valueNamesApp(fv, slug, depth+1) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if valueNamesApp(v.Index(i), slug, depth+1) {
				return true
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			if valueNamesApp(iter.Value(), slug, depth+1) {
				return true
			}
		}
	}
	return false
}
