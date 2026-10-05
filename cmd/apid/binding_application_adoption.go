// adr: 432 — report version-bound application receipts separately from probes.
package main

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) applyBindingApplicationAdoption(parent context.Context, acct state.Account, app state.App, inventory *api.AppBindingInventory, selectors map[string]state.BindingAdoptionSelector) {
	if len(selectors) == 0 {
		return
	}
	selected := make([]state.BindingAdoptionSelector, 0, len(selectors))
	for _, selector := range selectors {
		selected = append(selected, selector)
	}
	ctx, cancel := context.WithTimeout(parent, bindingInventoryReadTimeout)
	defer cancel()
	store, available := s.store.(state.BindingApplicationAdoptionStore)
	var rows []state.BindingApplicationAdoptionRow
	if available {
		var err error
		rows, err = store.ReadBindingApplicationAdoption(ctx, acct.ID, app.ID, selected)
		available = err == nil
	}
	now := time.Now().UTC()
	for index := range inventory.Bindings {
		item := &inventory.Bindings[index]
		selector, found := selectors[bindingVerificationKey(item.Type, item.Binding, item.Scope)]
		if !found {
			continue
		}
		adoption := bindingApplicationAdoption(selector, rows, available, now)
		item.ApplicationAdoption = &adoption
	}
	if !available {
		inventory.Warnings = append(inventory.Warnings, "Application adoption observations could not be read; strict application acknowledgement checks will block.")
	}
}

func bindingApplicationAdoption(selector state.BindingAdoptionSelector, rows []state.BindingApplicationAdoptionRow, complete bool, now time.Time) api.BindingApplicationAdoption {
	adoption := api.BindingApplicationAdoption{Source: "application_ack", ObservedAt: now, Complete: complete, SecretsExpected: len(selector.Keys), Targets: []api.BindingApplicationAckTarget{}}
	versions := map[string]int64{}
	for _, row := range rows {
		if row.Type != selector.Type || row.BindingID != selector.BindingID || row.Scope != selector.Scope || !slices.Contains(selector.Keys, row.Key) {
			continue
		}
		if old, exists := versions[row.Key]; exists && old != row.CurrentVersion {
			adoption.Complete = false
		}
		versions[row.Key] = row.CurrentVersion
		if row.CurrentVersion < 1 {
			adoption.Complete = false
		}
		if row.InstanceID == "" {
			if row.DeploymentID != "" || row.WorkloadName != "" {
				adoption.Complete = false
			}
			continue
		}
		adoption.Targets = append(adoption.Targets, api.BindingApplicationAckTarget{
			DeploymentID: row.DeploymentID, InstanceID: row.InstanceID, WorkloadName: row.WorkloadName,
			RuntimeState: row.RuntimeState, Key: row.Key, CurrentVersion: row.CurrentVersion, ReloadSupport: row.ReloadSupport,
			ReloadVersion: row.ReloadVersion, Projection: row.Projection, Signal: row.Signal, ReloadAt: row.ReloadAt,
			ApplicationAckVersion: row.ApplicationAckVersion, ApplicationAck: row.ApplicationAck, ApplicationAckAt: row.ApplicationAckAt,
			ProcessGeneration: row.ProcessGeneration, ApplicationAckGeneration: row.ApplicationAckGeneration,
		})
	}
	adoption.SecretsObserved = len(versions)
	return bindingcheck.SummarizeApplicationAdoption(adoption, now)
}
