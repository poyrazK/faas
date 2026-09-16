from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.applied_build import AppliedBuild
    from ..models.apply_response_apps_item import ApplyResponseAppsItem
    from ..models.plan_affected_app import PlanAffectedApp
    from ..models.plan_cron import PlanCron
    from ..models.plan_detection_warning import PlanDetectionWarning
    from ..models.plan_managed import PlanManaged
    from ..models.plan_workload import PlanWorkload


T = TypeVar("T", bound="ApplyResponse")


@_attrs_define
class ApplyResponse:
    """Apply response. Carries the inserted project_id and per-app IDs."""

    project_slug: str
    scan_source: str
    can_apply: bool
    plan_token: str
    repo_full_name: str | Unset = UNSET
    tier: str | Unset = UNSET
    workloads: list[PlanWorkload] | Unset = UNSET
    managed: list[PlanManaged] | Unset = UNSET
    crons: list[PlanCron] | Unset = UNSET
    warnings: list[str] | Unset = UNSET
    detection_warnings: list[PlanDetectionWarning] | Unset = UNSET
    """Structured skipped/merged detector decisions returned with the applied plan."""
    observed_apps: int | Unset = UNSET
    observed_crons: int | Unset = UNSET
    limit_apps: int | Unset = UNSET
    limit_crons: int | Unset = UNSET
    crons_not_allowed: bool | Unset = UNSET
    can_apply_pre_exclude: bool | Unset = UNSET
    gate_rescued_by_exclude: bool | Unset = UNSET
    can_apply_reasons: list[str] | Unset = UNSET
    will_deploy: list[PlanAffectedApp] | Unset = UNSET
    unaffected: list[PlanAffectedApp] | Unset = UNSET
    skipped: list[PlanAffectedApp] | Unset = UNSET
    removed: list[str] | Unset = UNSET
    persisted_exclusions: list[str] | Unset = UNSET
    stale_persisted_exclusions: list[str] | Unset = UNSET
    project_id: str | Unset = UNSET
    apps: list[ApplyResponseAppsItem] | Unset = UNSET
    builds: list[AppliedBuild] | Unset = UNSET
    """Per-workload build enqueue results. Populated when the apply
    path actually enqueued one (deployment, build) per added or
    changed workload (PR-A, repo decomposition Phase 5 close-
    the-loop). On staging or enqueue failure the per-app Error
    field is populated and the IDs are empty.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_slug = self.project_slug

        scan_source = self.scan_source

        can_apply = self.can_apply

        plan_token = self.plan_token

        repo_full_name = self.repo_full_name

        tier = self.tier

        workloads: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workloads, Unset):
            workloads = []
            for workloads_item_data in self.workloads:
                workloads_item = workloads_item_data.to_dict()
                workloads.append(workloads_item)

        managed: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.managed, Unset):
            managed = []
            for managed_item_data in self.managed:
                managed_item = managed_item_data.to_dict()
                managed.append(managed_item)

        crons: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.crons, Unset):
            crons = []
            for crons_item_data in self.crons:
                crons_item = crons_item_data.to_dict()
                crons.append(crons_item)

        warnings: list[str] | Unset = UNSET
        if not isinstance(self.warnings, Unset):
            warnings = self.warnings

        detection_warnings: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.detection_warnings, Unset):
            detection_warnings = []
            for detection_warnings_item_data in self.detection_warnings:
                detection_warnings_item = detection_warnings_item_data.to_dict()
                detection_warnings.append(detection_warnings_item)

        observed_apps = self.observed_apps

        observed_crons = self.observed_crons

        limit_apps = self.limit_apps

        limit_crons = self.limit_crons

        crons_not_allowed = self.crons_not_allowed

        can_apply_pre_exclude = self.can_apply_pre_exclude

        gate_rescued_by_exclude = self.gate_rescued_by_exclude

        can_apply_reasons: list[str] | Unset = UNSET
        if not isinstance(self.can_apply_reasons, Unset):
            can_apply_reasons = self.can_apply_reasons

        will_deploy: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.will_deploy, Unset):
            will_deploy = []
            for will_deploy_item_data in self.will_deploy:
                will_deploy_item = will_deploy_item_data.to_dict()
                will_deploy.append(will_deploy_item)

        unaffected: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.unaffected, Unset):
            unaffected = []
            for unaffected_item_data in self.unaffected:
                unaffected_item = unaffected_item_data.to_dict()
                unaffected.append(unaffected_item)

        skipped: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.skipped, Unset):
            skipped = []
            for skipped_item_data in self.skipped:
                skipped_item = skipped_item_data.to_dict()
                skipped.append(skipped_item)

        removed: list[str] | Unset = UNSET
        if not isinstance(self.removed, Unset):
            removed = self.removed

        persisted_exclusions: list[str] | Unset = UNSET
        if not isinstance(self.persisted_exclusions, Unset):
            persisted_exclusions = self.persisted_exclusions

        stale_persisted_exclusions: list[str] | Unset = UNSET
        if not isinstance(self.stale_persisted_exclusions, Unset):
            stale_persisted_exclusions = self.stale_persisted_exclusions

        project_id = self.project_id

        apps: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.apps, Unset):
            apps = []
            for apps_item_data in self.apps:
                apps_item = apps_item_data.to_dict()
                apps.append(apps_item)

        builds: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.builds, Unset):
            builds = []
            for builds_item_data in self.builds:
                builds_item = builds_item_data.to_dict()
                builds.append(builds_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_slug": project_slug,
                "scan_source": scan_source,
                "can_apply": can_apply,
                "plan_token": plan_token,
            }
        )
        if repo_full_name is not UNSET:
            field_dict["repo_full_name"] = repo_full_name
        if tier is not UNSET:
            field_dict["tier"] = tier
        if workloads is not UNSET:
            field_dict["workloads"] = workloads
        if managed is not UNSET:
            field_dict["managed"] = managed
        if crons is not UNSET:
            field_dict["crons"] = crons
        if warnings is not UNSET:
            field_dict["warnings"] = warnings
        if detection_warnings is not UNSET:
            field_dict["detection_warnings"] = detection_warnings
        if observed_apps is not UNSET:
            field_dict["observed_apps"] = observed_apps
        if observed_crons is not UNSET:
            field_dict["observed_crons"] = observed_crons
        if limit_apps is not UNSET:
            field_dict["limit_apps"] = limit_apps
        if limit_crons is not UNSET:
            field_dict["limit_crons"] = limit_crons
        if crons_not_allowed is not UNSET:
            field_dict["crons_not_allowed"] = crons_not_allowed
        if can_apply_pre_exclude is not UNSET:
            field_dict["can_apply_pre_exclude"] = can_apply_pre_exclude
        if gate_rescued_by_exclude is not UNSET:
            field_dict["gate_rescued_by_exclude"] = gate_rescued_by_exclude
        if can_apply_reasons is not UNSET:
            field_dict["can_apply_reasons"] = can_apply_reasons
        if will_deploy is not UNSET:
            field_dict["will_deploy"] = will_deploy
        if unaffected is not UNSET:
            field_dict["unaffected"] = unaffected
        if skipped is not UNSET:
            field_dict["skipped"] = skipped
        if removed is not UNSET:
            field_dict["removed"] = removed
        if persisted_exclusions is not UNSET:
            field_dict["persisted_exclusions"] = persisted_exclusions
        if stale_persisted_exclusions is not UNSET:
            field_dict["stale_persisted_exclusions"] = stale_persisted_exclusions
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if apps is not UNSET:
            field_dict["apps"] = apps
        if builds is not UNSET:
            field_dict["builds"] = builds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.applied_build import AppliedBuild
        from ..models.apply_response_apps_item import ApplyResponseAppsItem
        from ..models.plan_affected_app import PlanAffectedApp
        from ..models.plan_cron import PlanCron
        from ..models.plan_detection_warning import PlanDetectionWarning
        from ..models.plan_managed import PlanManaged
        from ..models.plan_workload import PlanWorkload

        d = dict(src_dict)
        project_slug = d.pop("project_slug")

        scan_source = d.pop("scan_source")

        can_apply = d.pop("can_apply")

        plan_token = d.pop("plan_token")

        repo_full_name = d.pop("repo_full_name", UNSET)

        tier = d.pop("tier", UNSET)

        _workloads = d.pop("workloads", UNSET)
        workloads: list[PlanWorkload] | Unset = UNSET
        if _workloads is not UNSET:
            workloads = []
            for workloads_item_data in _workloads:
                workloads_item = PlanWorkload.from_dict(workloads_item_data)

                workloads.append(workloads_item)

        _managed = d.pop("managed", UNSET)
        managed: list[PlanManaged] | Unset = UNSET
        if _managed is not UNSET:
            managed = []
            for managed_item_data in _managed:
                managed_item = PlanManaged.from_dict(managed_item_data)

                managed.append(managed_item)

        _crons = d.pop("crons", UNSET)
        crons: list[PlanCron] | Unset = UNSET
        if _crons is not UNSET:
            crons = []
            for crons_item_data in _crons:
                crons_item = PlanCron.from_dict(crons_item_data)

                crons.append(crons_item)

        warnings = cast(list[str], d.pop("warnings", UNSET))

        _detection_warnings = d.pop("detection_warnings", UNSET)
        detection_warnings: list[PlanDetectionWarning] | Unset = UNSET
        if _detection_warnings is not UNSET:
            detection_warnings = []
            for detection_warnings_item_data in _detection_warnings:
                detection_warnings_item = PlanDetectionWarning.from_dict(detection_warnings_item_data)

                detection_warnings.append(detection_warnings_item)

        observed_apps = d.pop("observed_apps", UNSET)

        observed_crons = d.pop("observed_crons", UNSET)

        limit_apps = d.pop("limit_apps", UNSET)

        limit_crons = d.pop("limit_crons", UNSET)

        crons_not_allowed = d.pop("crons_not_allowed", UNSET)

        can_apply_pre_exclude = d.pop("can_apply_pre_exclude", UNSET)

        gate_rescued_by_exclude = d.pop("gate_rescued_by_exclude", UNSET)

        can_apply_reasons = cast(list[str], d.pop("can_apply_reasons", UNSET))

        _will_deploy = d.pop("will_deploy", UNSET)
        will_deploy: list[PlanAffectedApp] | Unset = UNSET
        if _will_deploy is not UNSET:
            will_deploy = []
            for will_deploy_item_data in _will_deploy:
                will_deploy_item = PlanAffectedApp.from_dict(will_deploy_item_data)

                will_deploy.append(will_deploy_item)

        _unaffected = d.pop("unaffected", UNSET)
        unaffected: list[PlanAffectedApp] | Unset = UNSET
        if _unaffected is not UNSET:
            unaffected = []
            for unaffected_item_data in _unaffected:
                unaffected_item = PlanAffectedApp.from_dict(unaffected_item_data)

                unaffected.append(unaffected_item)

        _skipped = d.pop("skipped", UNSET)
        skipped: list[PlanAffectedApp] | Unset = UNSET
        if _skipped is not UNSET:
            skipped = []
            for skipped_item_data in _skipped:
                skipped_item = PlanAffectedApp.from_dict(skipped_item_data)

                skipped.append(skipped_item)

        removed = cast(list[str], d.pop("removed", UNSET))

        persisted_exclusions = cast(list[str], d.pop("persisted_exclusions", UNSET))

        stale_persisted_exclusions = cast(list[str], d.pop("stale_persisted_exclusions", UNSET))

        project_id = d.pop("project_id", UNSET)

        _apps = d.pop("apps", UNSET)
        apps: list[ApplyResponseAppsItem] | Unset = UNSET
        if _apps is not UNSET:
            apps = []
            for apps_item_data in _apps:
                apps_item = ApplyResponseAppsItem.from_dict(apps_item_data)

                apps.append(apps_item)

        _builds = d.pop("builds", UNSET)
        builds: list[AppliedBuild] | Unset = UNSET
        if _builds is not UNSET:
            builds = []
            for builds_item_data in _builds:
                builds_item = AppliedBuild.from_dict(builds_item_data)

                builds.append(builds_item)

        apply_response = cls(
            project_slug=project_slug,
            scan_source=scan_source,
            can_apply=can_apply,
            plan_token=plan_token,
            repo_full_name=repo_full_name,
            tier=tier,
            workloads=workloads,
            managed=managed,
            crons=crons,
            warnings=warnings,
            detection_warnings=detection_warnings,
            observed_apps=observed_apps,
            observed_crons=observed_crons,
            limit_apps=limit_apps,
            limit_crons=limit_crons,
            crons_not_allowed=crons_not_allowed,
            can_apply_pre_exclude=can_apply_pre_exclude,
            gate_rescued_by_exclude=gate_rescued_by_exclude,
            can_apply_reasons=can_apply_reasons,
            will_deploy=will_deploy,
            unaffected=unaffected,
            skipped=skipped,
            removed=removed,
            persisted_exclusions=persisted_exclusions,
            stale_persisted_exclusions=stale_persisted_exclusions,
            project_id=project_id,
            apps=apps,
            builds=builds,
        )

        apply_response.additional_properties = d
        return apply_response

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
