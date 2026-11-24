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

// errDeployToAlternativeDeclined stops a deploy whose user declined to
// replace the running release of their existing suffixed app.
var errDeployToAlternativeDeclined = errors.New("deploy cancelled")

// createOrFetchAppWithAlternatives is createOrFetchAppWith that, when another
// owner holds req.Slug, deploys under a suffixed alternative instead of
// failing (api.SuffixedAppSlugs). The alternatives are the same for the same
// owner and name every time, so an alternative the caller already holds (an
// earlier deploy took it; a fresh checkout has no link to it) is updated, a
// free one is created, and one another owner holds is skipped. It returns the
// slug the app has.
//
// A request that does not conflict makes exactly the calls it made before.
// The conflict error stays when gregale.yaml in sourceDir names the app (its
// triggers, routes and bindings would no longer match another name) or the
// owner cannot be read. confirmExisting, when set, may decline updating an
// existing alternative (the unlinked-app guard). An alternative held by one
// of the caller's own deleted apps is skipped. unavailable reports that the
// requested name is not offered (app_name_blocklist) rather than held by
// another owner.
func createOrFetchAppWithAlternatives(ctx context.Context, client *Client, req, existingReq api.CreateAppRequest, sourceDir string, confirmExisting func(slug string) bool, requireAuthnPtr *bool, appProtocolPtr *string, publicAuthPtr *api.PublicAuthBlock, resolved *api.AppResponse) (slug string, unavailable bool, err error) {
	err = createOrFetchAppWith(ctx, client, req, existingReq, requireAuthnPtr, appProtocolPtr, publicAuthPtr, resolved)
	var taken *slugTakenError
	if !errors.As(err, &taken) {
		return req.Slug, false, err
	}
	unavailable = taken.unavailable
	if manifestNamesApp(sourceDir, req.Slug) {
		return req.Slug, unavailable, err
	}
	acct, whoErr := client.Whoami(ctx)
	if whoErr != nil || acct.ID == "" {
		return req.Slug, unavailable, err
	}
	// An organization key creates the app in the organization, so every
	// member deploying the same checkout gets the same alternatives.
	owner := acct.ID
	if acct.KeyOrg != nil && acct.KeyOrg.ID != "" {
		owner = "org:" + acct.KeyOrg.ID
	}
	alternatives := api.SuffixedAppSlugs(owner, req.Slug, appSlugAlternativeCount)
	for _, alt := range alternatives {
		if confirmExisting != nil {
			if _, getErr := client.GetApp(ctx, alt); getErr == nil && !confirmExisting(alt) {
				return alt, unavailable, errDeployToAlternativeDeclined
			}
		}
		altReq, altExisting := req, existingReq
		altReq.Slug, altExisting.Slug = alt, alt
		err = createOrFetchAppWith(ctx, client, altReq, altExisting, requireAuthnPtr, appProtocolPtr, publicAuthPtr, resolved)
		// An alternative the caller's own deleted app holds (they deleted
		// the suffixed app) is skipped like a taken one: the deploy takes
		// the next alternative instead of failing.
		var ownDeleted *ownDeletedSlugError
		if !errors.As(err, &taken) && !errors.As(err, &ownDeleted) {
			return alt, unavailable, err
		}
	}
	if unavailable {
		return req.Slug, true, fmt.Errorf("app name %q is not available, nor are its alternatives %s; pick a different --name", req.Slug, strings.Join(alternatives, ", "))
	}
	return req.Slug, false, fmt.Errorf("slug %q and its alternatives %s are already in use; pick a different --name", req.Slug, strings.Join(alternatives, ", "))
}

// printSlugAlternativeNotice tells the user their app has a different name
// from the one they asked for, and how to change it.
func printSlugAlternativeNotice(requested, got string, unavailable bool) {
	if jsonOutput || requested == got {
		return
	}
	if unavailable {
		_, _ = fmt.Fprintf(osStderr, "Note: the name %s is not available, so this app is %s (%s). Pass --name to choose a different name.\n", requested, got, deployedAppURL(got))
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
		// Do not rename an app whose manifest could not be checked.
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
