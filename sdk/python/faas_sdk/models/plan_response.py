from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.plan_response_scan_source import PlanResponseScanSource, check_plan_response_scan_source
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.plan_affected_app import PlanAffectedApp
    from ..models.plan_cron import PlanCron
    from ..models.plan_detection_warning import PlanDetectionWarning
    from ..models.plan_managed import PlanManaged
    from ..models.plan_workload import PlanWorkload


T = TypeVar("T", bound="PlanResponse")


@_attrs_define
class PlanResponse:
    """Dry-run scan response."""

    project_slug: str
    scan_source: PlanResponseScanSource
    tier: str
    workloads: list[PlanWorkload]
    managed: list[PlanManaged]
    crons: list[PlanCron]
    observed_apps: int
    observed_crons: int
    limit_apps: int
    limit_crons: int
    can_apply: bool
    plan_token: str
    """base64-JSON plan token; pass back as ?plan_token= on /v1/projects to skip the second extract."""
    repo_full_name: str | Unset = UNSET
    environment: str | Unset = UNSET
    """Registered project environment targeted by this plan"""
    environment_protected: bool | Unset = UNSET
    """Whether applying this plan requires protected-environment approval"""
    environment_config_hash: str | Unset = UNSET
    """Hash of the non-secret environment configuration bound into this plan"""
    warnings: list[str] | Unset = UNSET
    detection_warnings: list[PlanDetectionWarning] | Unset = UNSET
    """Structured skipped/merged detector decisions available to explain-mode clients."""
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
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        project_slug = self.project_slug

        scan_source: str = self.scan_source

        tier = self.tier

        workloads = []
        for workloads_item_data in self.workloads:
            workloads_item = workloads_item_data.to_dict()
            workloads.append(workloads_item)

        managed = []
        for managed_item_data in self.managed:
            managed_item = managed_item_data.to_dict()
            managed.append(managed_item)

        crons = []
        for crons_item_data in self.crons:
            crons_item = crons_item_data.to_dict()
            crons.append(crons_item)

        observed_apps = self.observed_apps

        observed_crons = self.observed_crons

        limit_apps = self.limit_apps

        limit_crons = self.limit_crons

        can_apply = self.can_apply

        plan_token = self.plan_token

        repo_full_name = self.repo_full_name

        environment = self.environment

        environment_protected = self.environment_protected

        environment_config_hash = self.environment_config_hash

        warnings: list[str] | Unset = UNSET
        if not isinstance(self.warnings, Unset):
            warnings = self.warnings

        detection_warnings: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.detection_warnings, Unset):
            detection_warnings = []
            for detection_warnings_item_data in self.detection_warnings:
                detection_warnings_item = detection_warnings_item_data.to_dict()
                detection_warnings.append(detection_warnings_item)

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

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "project_slug": project_slug,
                "scan_source": scan_source,
                "tier": tier,
                "workloads": workloads,
                "managed": managed,
                "crons": crons,
                "observed_apps": observed_apps,
                "observed_crons": observed_crons,
                "limit_apps": limit_apps,
                "limit_crons": limit_crons,
                "can_apply": can_apply,
                "plan_token": plan_token,
            }
        )
        if repo_full_name is not UNSET:
            field_dict["repo_full_name"] = repo_full_name
        if environment is not UNSET:
            field_dict["environment"] = environment
        if environment_protected is not UNSET:
            field_dict["environment_protected"] = environment_protected
        if environment_config_hash is not UNSET:
            field_dict["environment_config_hash"] = environment_config_hash
        if warnings is not UNSET:
            field_dict["warnings"] = warnings
        if detection_warnings is not UNSET:
            field_dict["detection_warnings"] = detection_warnings
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.plan_affected_app import PlanAffectedApp
        from ..models.plan_cron import PlanCron
        from ..models.plan_detection_warning import PlanDetectionWarning
        from ..models.plan_managed import PlanManaged
        from ..models.plan_workload import PlanWorkload

        d = dict(src_dict)
        project_slug = d.pop("project_slug")

        scan_source = check_plan_response_scan_source(d.pop("scan_source"))

        tier = d.pop("tier")

        workloads = []
        _workloads = d.pop("workloads")
        for workloads_item_data in _workloads:
            workloads_item = PlanWorkload.from_dict(workloads_item_data)

            workloads.append(workloads_item)

        managed = []
        _managed = d.pop("managed")
        for managed_item_data in _managed:
            managed_item = PlanManaged.from_dict(managed_item_data)

            managed.append(managed_item)

        crons = []
        _crons = d.pop("crons")
        for crons_item_data in _crons:
            crons_item = PlanCron.from_dict(crons_item_data)

            crons.append(crons_item)

        observed_apps = d.pop("observed_apps")

        observed_crons = d.pop("observed_crons")

        limit_apps = d.pop("limit_apps")

        limit_crons = d.pop("limit_crons")

        can_apply = d.pop("can_apply")

        plan_token = d.pop("plan_token")

        repo_full_name = d.pop("repo_full_name", UNSET)

        environment = d.pop("environment", UNSET)

        environment_protected = d.pop("environment_protected", UNSET)

        environment_config_hash = d.pop("environment_config_hash", UNSET)

        warnings = cast(list[str], d.pop("warnings", UNSET))

        _detection_warnings = d.pop("detection_warnings", UNSET)
        detection_warnings: list[PlanDetectionWarning] | Unset = UNSET
        if _detection_warnings is not UNSET:
            detection_warnings = []
            for detection_warnings_item_data in _detection_warnings:
                detection_warnings_item = PlanDetectionWarning.from_dict(detection_warnings_item_data)

                detection_warnings.append(detection_warnings_item)

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

        plan_response = cls(
            project_slug=project_slug,
            scan_source=scan_source,
            tier=tier,
            workloads=workloads,
            managed=managed,
            crons=crons,
            observed_apps=observed_apps,
            observed_crons=observed_crons,
            limit_apps=limit_apps,
            limit_crons=limit_crons,
            can_apply=can_apply,
            plan_token=plan_token,
            repo_full_name=repo_full_name,
            environment=environment,
            environment_protected=environment_protected,
            environment_config_hash=environment_config_hash,
            warnings=warnings,
            detection_warnings=detection_warnings,
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
        )

        plan_response.additional_properties = d
        return plan_response

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
