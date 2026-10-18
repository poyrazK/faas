package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type startTarget struct {
	source string
	app    api.AppResponse
	exists bool
}

func (r *startRunner) resolveLaunchTarget() error {
	if r.target != nil && r.target.source == r.session.SourcePath {
		return nil
	}
	if r.session.AppSlug == "" {
		r.session.AppSlug = sanitizeProjectSlug(sourceNameForPath(r.session.SourcePath))
	}
	if err := r.resolveStartContext(); err != nil {
		return err
	}
	for {
		if !api.ValidAppSlug(r.session.AppSlug) {
			if err := r.chooseAppName(); err != nil {
				return err
			}
		}
		app, err := r.getLaunchApp(r.ctx, r.client, r.session.AppSlug)
		exists := err == nil
		var problem *APIError
		if err != nil && (!errors.As(err, &problem) || problem.Problem.Status != http.StatusNotFound) {
			return fmt.Errorf("check app name: %w", err)
		}
		if r.session.SourceContext != "" && !exists {
			return errors.New("the saved app is unavailable; run gregale context --verify, or unlink before choosing a new app")
		}
		if r.session.AppID != "" && (!exists || app.ID != r.session.AppID) {
			return errors.New("app identity changed; this launch must target the app recorded by its session")
		}
		if !exists && api.IsReservedAppSlug(r.session.AppSlug) {
			PrintWarn(osStdout, "This app name is reserved by the platform.")
			if err := r.chooseAppName(); err != nil {
				return err
			}
			continue
		}
		if exists && r.session.AppID == "" && r.session.SourceContext == "" {
			reuse, err := r.chooseExistingTarget()
			if err != nil {
				return err
			}
			if !reuse {
				continue
			}
		}
		r.target = &startTarget{source: r.session.SourcePath, app: app, exists: exists}
		if exists {
			r.session.AppID = app.ID
		}
		return nil
	}
}

func (r *startRunner) chooseExistingTarget() (bool, error) {
	choice, err := r.prompt.choose(r.ctx, "App "+r.session.AppSlug+" already exists in this account.", []string{"Create an app with another name", "Deploy a new revision to " + r.session.AppSlug, "Finish"}, 0)
	if err != nil {
		return false, err
	}
	switch choice {
	case 1:
		return true, nil
	case 2:
		return false, io.EOF
	}
	return false, r.chooseAppName()
}

func (r *startRunner) chooseAppName() error {
	fallback := strings.TrimRight(r.session.AppSlug[:min(len(r.session.AppSlug), 38)], "-") + "-2"
	if !api.ValidAppSlug(fallback) || api.IsReservedAppSlug(fallback) {
		fallback = "my-app"
	}
	for {
		name, err := r.prompt.text(r.ctx, "New app name", fallback)
		if err != nil {
			return err
		}
		if api.ValidAppSlug(name) && !api.IsReservedAppSlug(name) {
			r.session.AppSlug = name
			return nil
		}
		PrintWarn(osStdout, "Use an available name with 3–40 lowercase letters, digits, or internal hyphens; reserved platform names are unavailable.")
	}
}

// Enforce the reviewed selection at the shared deploy's app-resolution step.
// A create conflict must never fall back to updating an unrelated app.
func (r *startRunner) resolveDeployTarget(ctx context.Context, client *Client, req api.CreateAppRequest) (api.AppResponse, error) {
	if apiBase() != r.session.APIBase {
		return api.AppResponse{}, errors.New("API changed after review; start again to review the current connection")
	}
	// Deploy reloads credentials. Verify that those credentials still select
	// the reviewed account before creating an app or configuring secrets.
	account, err := client.Whoami(ctx)
	if err != nil {
		return api.AppResponse{}, fmt.Errorf("verify reviewed account: %w", err)
	}
	if account.ID != r.account.ID {
		return api.AppResponse{}, errors.New("account changed after review; start again to review the current connection")
	}
	_, key, err := startSourceContext(r.session.SourcePath)
	if err != nil {
		return api.AppResponse{}, err
	}
	if key != r.session.SourceContext {
		return api.AppResponse{}, errors.New("linked target changed after review; start again to review it")
	}
	if err := r.verifyReviewedSource(ctx); err != nil {
		return api.AppResponse{}, err
	}
	if err := r.verifyStartProject(ctx, client); err != nil {
		return api.AppResponse{}, err
	}
	if err := r.requireStartCompatibility(ctx, client); err != nil {
		return api.AppResponse{}, err
	}
	if !r.target.exists {
		return client.CreateApp(ctx, req)
	}
	app, err := r.getLaunchApp(ctx, client, req.Slug)
	if err != nil {
		return app, err
	}
	if app.ID != r.target.app.ID {
		return app, errors.New("app identity changed after review; start again to review the current target")
	}
	return app, nil
}
