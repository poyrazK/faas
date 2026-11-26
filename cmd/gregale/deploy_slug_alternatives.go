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

// errDeployToAlternativeDeclined stops a deploy whose user declined to
// replace the running release of their existing suffixed app.
var errDeployToAlternativeDeclined = errors.New("deploy cancelled")

// appNameChoice is the app a deploy resolved its --name to.
type appNameChoice struct {
	// Slug is the app's name: the one asked for or a suffixed alternative.
	Slug string
	// Unavailable: the name asked for is not offered (app_name_blocklist)
	// rather than held by another owner.
	Unavailable bool
	// Adopted: the caller has no app under the name asked for, so the deploy
	// updates the suffixed app an earlier deploy of the name took, without
	// asking whether the name is free now.
	Adopted bool
}

// createOrFetchAppWithAlternatives is createOrFetchAppWith that, when another
// owner holds req.Slug, deploys under a suffixed alternative instead of
// failing (api.SuffixedAppSlugs). The alternatives are the same for the same
// owner and name every time, so an alternative the caller already holds (an
// earlier deploy took it; a fresh checkout has no link to it) is updated, a
// free one is created, and one another owner holds is skipped.
//
// The caller's suffixed app is looked for before req.Slug is created, so a
// fresh checkout keeps deploying to it after the name it stands in for frees
// up, instead of creating a second app under that name. A deploy to an app
// the caller has under req.Slug makes exactly the calls it made before. The
// conflict error stays when gregale.yaml in sourceDir names the app (its
// triggers, routes and bindings would no longer match another name) or the
// owner cannot be read. confirmExisting, when set, may decline updating an
// existing alternative (the unlinked-app guard). An alternative held by one
// of the caller's own deleted apps is skipped.
func createOrFetchAppWithAlternatives(ctx context.Context, client *Client, req, existingReq api.CreateAppRequest, sourceDir string, confirmExisting func(slug string) bool, requireAuthnPtr *bool, appProtocolPtr *string, publicAuthPtr *api.PublicAuthBlock, resolved *api.AppResponse) (appNameChoice, error) {
	var taken *slugTakenError
	if manifestNamesApp(sourceDir, req.Slug) {
		slug, err := createOrFetchAppAdopting(ctx, client, req, existingReq, nil, requireAuthnPtr, appProtocolPtr, publicAuthPtr, resolved)
		return appNameChoice{Slug: slug, Unavailable: errors.As(err, &taken) && taken.unavailable}, err
	}
	var alternatives []string
	ownerKnown := func() bool {
		if alternatives == nil {
			owner, err := deployOwner(ctx, client)
			if err != nil {
				return false
			}
			alternatives = api.SuffixedAppSlugs(owner, req.Slug, api.AppSlugAlternatives)
		}
		return true
	}
	adopt := func() (api.AppResponse, bool, error) {
		if !ownerKnown() {
			return api.AppResponse{}, false, nil
		}
		for _, alt := range alternatives {
			app, err := client.GetApp(ctx, alt)
			if err != nil {
				continue
			}
			if confirmExisting != nil && !confirmExisting(alt) {
				return app, false, errDeployToAlternativeDeclined
			}
			return app, true, nil
		}
		return api.AppResponse{}, false, nil
	}
	slug, err := createOrFetchAppAdopting(ctx, client, req, existingReq, adopt, requireAuthnPtr, appProtocolPtr, publicAuthPtr, resolved)
	if errors.Is(err, errDeployToAlternativeDeclined) {
		return appNameChoice{Slug: slug}, err
	}
	if slug != req.Slug {
		return appNameChoice{Slug: slug, Adopted: true}, err
	}
	if !errors.As(err, &taken) {
		return appNameChoice{Slug: slug}, err
	}
	unavailable := taken.unavailable
	if !ownerKnown() {
		return appNameChoice{Slug: req.Slug, Unavailable: unavailable}, err
	}
	// adopt found none of the alternatives: each is created or skipped.
	for _, alt := range alternatives {
		altReq, altExisting := req, existingReq
		altReq.Slug, altExisting.Slug = alt, alt
		err = createOrFetchAppWith(ctx, client, altReq, altExisting, requireAuthnPtr, appProtocolPtr, publicAuthPtr, resolved)
		// An alternative the caller's own deleted app holds (they deleted
		// the suffixed app) is skipped like a taken one: the deploy takes
		// the next alternative instead of failing.
		var ownDeleted *ownDeletedSlugError
		if !errors.As(err, &taken) && !errors.As(err, &ownDeleted) {
			return appNameChoice{Slug: alt, Unavailable: unavailable}, err
		}
	}
	if unavailable {
		return appNameChoice{Slug: req.Slug, Unavailable: true}, fmt.Errorf("app name %q is not available, nor are its alternatives %s; pick a different --name", req.Slug, strings.Join(alternatives, ", "))
	}
	return appNameChoice{Slug: req.Slug}, fmt.Errorf("slug %q and its alternatives %s are already in use; pick a different --name", req.Slug, strings.Join(alternatives, ", "))
}

// deployOwner is the owner api.SuffixedAppSlugs derives a deploy's
// alternatives from. An organization key creates the app in the
// organization, so every member deploying the same checkout gets the same
// alternatives.
func deployOwner(ctx context.Context, client *Client) (string, error) {
	acct, err := client.Whoami(ctx)
	if err != nil {
		return "", err
	}
	if acct.KeyOrg != nil && acct.KeyOrg.ID != "" {
		return "org:" + acct.KeyOrg.ID, nil
	}
	if acct.ID == "" {
		return "", errors.New("account has no id")
	}
	return acct.ID, nil
}

// printSlugAlternativeNotice tells the user their app has a different name
// from the one they asked for, and how to change it.
func printSlugAlternativeNotice(requested string, got appNameChoice) {
	if jsonOutput || requested == got.Slug {
		return
	}
	switch {
	case got.Adopted:
		_, _ = fmt.Fprintf(osStderr, "Note: the name %s deploys to your app %s (%s), which an earlier deploy created when %s was taken. To move it to %s once that name is free, run `gregale app %s rename %s`.\n", requested, got.Slug, deployedAppURL(got.Slug), requested, requested, got.Slug, requested)
	case got.Unavailable:
		_, _ = fmt.Fprintf(osStderr, "Note: the name %s is not available, so this app is %s (%s). Pass --name to choose a different name.\n", requested, got.Slug, deployedAppURL(got.Slug))
	default:
		_, _ = fmt.Fprintf(osStderr, "Note: the name %s belongs to another account, so this app is %s (%s). Pass --name to choose a different name.\n", requested, got.Slug, deployedAppURL(got.Slug))
	}
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
