//go:build linux

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

func (g *databaseGuest) manageExtensions(req dbguest.Request, resp dbguest.Response) dbguest.Response {
	if resp.State != dbguest.StateReady || req.EnableExtension != "" && (resp.MigrationFenced || resp.MigrationRunning) {
		resp.Error = dbguest.ErrCodeNotReady
		return resp
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	catalog, ok := g.readExtensions(ctx, req.DiagnosticDatabase)
	if !ok {
		resp.Error = dbguest.ErrCodeNotReady
		return resp
	}
	name := req.EnableExtension
	if name != "" && !slices.ContainsFunc(catalog.Installed, func(i pgextension.Installed) bool { return i.Name == name }) {
		if !slices.ContainsFunc(catalog.Available, func(a pgextension.Available) bool { return a.Name == name && a.Default }) {
			resp.Error = dbguest.ErrCodeExtension
			return resp
		}
		// Only fixed extension installers may run through this connection.
		sql, valid := pgextension.EnableSQL(name)
		if !valid {
			resp.Error = dbguest.ErrCodeInvalidRequest
			return resp
		}
		if _, err := g.queryIn(ctx, req.DiagnosticDatabase, sql); err != nil {
			resp.Error = dbguest.ErrCodeNotReady
			return resp
		}
		catalog, ok = g.readExtensions(ctx, req.DiagnosticDatabase)
		if !ok || !slices.ContainsFunc(catalog.Installed, func(i pgextension.Installed) bool { return i.Name == name }) {
			resp.Error = dbguest.ErrCodeNotReady
			return resp
		}
	}
	resp.Extensions = &catalog
	return resp
}

func (g *databaseGuest) readExtensions(ctx context.Context, database string) (pgextension.Catalog, bool) {
	var catalog pgextension.Catalog
	raw, err := g.queryIn(ctx, database, pgextension.CatalogSQL)
	if err != nil || json.Unmarshal([]byte(raw), &catalog) != nil || !catalog.Valid() {
		return pgextension.Catalog{}, false
	}
	if !g.vectorLibraryAvailable() {
		catalog.Available = slices.DeleteFunc(catalog.Available, func(a pgextension.Available) bool {
			return a.Name == pgextension.VectorName
		})
	}
	return catalog, true
}

func (g *databaseGuest) vectorLibraryAvailable() bool {
	path := filepath.Join(filepath.Dir(databaseBinDir(g.major)), "lib", "vector.so")
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
