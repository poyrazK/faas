package main

// adr: 791
import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

type cancelMaintenanceLog struct{ cancel context.CancelFunc }

func (l cancelMaintenanceLog) Write(p []byte) (int, error) {
	l.cancel()
	return len(p), nil
}

func TestLayerCloneMaintenanceRefusesSweepWithoutNativeBarrier(t *testing.T) {
	for _, mode := range []string{"missing", "inventory-held"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			bucket := filepath.Join(root, "aa")
			if err := os.Mkdir(bucket, 0700); err != nil {
				t.Fatal(err)
			}
			clone := filepath.Join(bucket, ".faas-layer-"+uuid.NewString()+"-owned-test")
			if err := os.WriteFile(clone, []byte("retained clone"), 0600); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			if err := os.Chtimes(clone, old, old); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			calls := 0
			var maintain func(context.Context, func(context.Context) error) error
			if mode == "inventory-held" {
				maintain = func(context.Context, func(context.Context) error) error {
					cancel()
					return errors.New("inventory holds native effects")
				}
			}
			runLayerCloneMaintenance(ctx, slog.New(slog.NewTextHandler(cancelMaintenanceLog{cancel}, nil)), root, nil,
				func(context.Context, string) (bool, error) { calls++; return false, nil }, nil, maintain)
			if calls != 0 {
				t.Fatal("refused maintenance reached physical liveness gate")
			}
			if raw, err := os.ReadFile(clone); err != nil || string(raw) != "retained clone" {
				t.Fatal("refused maintenance removed clone", err)
			}
		})
	}
}
