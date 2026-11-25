package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

type autovacuumConfig struct {
	pgData   string
	file     string
	uid, gid int
}

func readAutovacuum(ctx context.Context, query func(context.Context, string) (string, error)) (dbguest.AutovacuumSettings, error) {
	var settings dbguest.AutovacuumSettings
	data, err := query(ctx, dbguest.AutovacuumSQL)
	if err != nil {
		return settings, err
	}
	err = json.Unmarshal([]byte(data), &settings)
	return settings, err
}

func (c autovacuumConfig) apply(ctx context.Context, profile dbguest.AutovacuumProfile, revision int64, query func(context.Context, string) (string, error)) (dbguest.AutovacuumSettings, error) {
	want, ok := profile.Settings()
	if !ok {
		return want, errors.New("unknown autovacuum profile")
	}
	if err := c.write(want, revision); err != nil {
		return want, err
	}
	current, err := readAutovacuum(ctx, query)
	if err == nil && current == want {
		return current, nil
	}
	if result, err := query(ctx, "SELECT pg_catalog.pg_reload_conf()"); err != nil || result != "t" {
		return current, errors.New("could not reload autovacuum settings")
	}
	timer := time.NewTicker(40 * time.Millisecond)
	defer timer.Stop()
	for {
		current, err := readAutovacuum(ctx, query)
		if err == nil && current == want {
			return current, nil
		}
		select {
		case <-ctx.Done():
			return current, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c autovacuumConfig) write(settings dbguest.AutovacuumSettings, revision int64) error {
	const header = "# Gregale autovacuum revision "
	previous, err := os.ReadFile(c.file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(previous) > 0 {
		line, _, _ := strings.Cut(string(previous), "\n")
		old, err := strconv.ParseInt(strings.TrimPrefix(line, header), 10, 64)
		if err != nil || old > revision {
			return errors.New("outdated autovacuum change")
		}
	}
	if err := c.ensureInclude(); err != nil {
		return err
	}
	config := fmt.Sprintf("%s%d\nautovacuum_vacuum_scale_factor = %g\nautovacuum_analyze_scale_factor = %g\n",
		header, revision, settings.VacuumScaleFactor, settings.AnalyzeScaleFactor)
	if string(previous) == config {
		return nil
	}
	return c.replace(c.file, config)
}

func (c autovacuumConfig) ensureInclude() error {
	path := filepath.Join(c.pgData, "postgresql.conf")
	relative, err := filepath.Rel(c.pgData, c.file)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	line := "\ninclude_if_exists = '" + relative + "'\n"
	if strings.Contains("\n"+string(data)+"\n", line) {
		return nil
	}
	return c.replace(path, string(data)+line)
}

func (c autovacuumConfig) replace(path, content string) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".gregale-autovacuum-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chown(c.uid, c.gid); err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
