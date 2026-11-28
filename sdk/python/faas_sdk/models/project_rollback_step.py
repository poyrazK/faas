from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_rollback_step_status import ProjectRollbackStepStatus, check_project_rollback_step_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_check_finding import BindingCheckFinding
    from ..models.problem import Problem
    from ..models.project_rollback_health import ProjectRollbackHealth
    from ..models.project_rollback_live_traffic import ProjectRollbackLiveTraffic
    from ..models.project_rollback_pre_cutover_health import ProjectRollbackPreCutoverHealth
    from ..models.project_rollback_traffic import ProjectRollbackTraffic
    from ..models.project_rollback_undo_artifacts import ProjectRollbackUndoArtifacts
    from ..models.rollback_crossed_release import RollbackCrossedRelease


T = TypeVar("T", bound="ProjectRollbackStep")


@_attrs_define
class ProjectRollbackStep:
    """Planned or observed rollback for one service and its exact revision pair."""

    service: str
    slug: str
    status: ProjectRollbackStepStatus
    from_revision: int | Unset = UNSET
    to_revision: int | Unset = UNSET
    reason: str | Unset = UNSET
    app_id: UUID | Unset = UNSET
    scope: str | Unset = UNSET
    expected_current_deployment_id: UUID | Unset = UNSET
    current_served_at: datetime.datetime | Unset = UNSET
    target_deployment_id: UUID | Unset = UNSET
    deployment_id: UUID | Unset = UNSET
    attempt_id: UUID | Unset = UNSET
    rollback_operation_id: UUID | Unset = UNSET
    preparation_status: str | Unset = UNSET
    """ Target preparation state such as warming, checking, ready, blocked, or released. """
    blockers: list[BindingCheckFinding] | Unset = UNSET
    warnings: list[BindingCheckFinding] | Unset = UNSET
    crossed_releases: list[RollbackCrossedRelease] | Unset = UNSET
    """ Release runs crossed by this undo step. """
    undo_artifacts: ProjectRollbackUndoArtifacts | Unset = UNSET
    """ The artifact hold for the exact deployment restored by an undo. """
    problem: Problem | Unset = UNSET
    """ RFC 9457 problem+json envelope. The `code` field is the stable
    machine-readable identifier; clients branch on it. `limit` and
    `observed` are populated on quota errors. `docs_url` points the
    user at the next action. Common customer-facing codes and recovery
    guidance are listed in `docs/errors.md`. `billing_portal_url` is
    populated on `code: payment_required` when the customer already has a
    provider subscription and must update it in the provider
    portal. `checkout_url` is populated when a new hosted checkout
    is required. `paddle_checkout_url` is retained as a legacy
    alias for Paddle clients, and `tx_id` carries the provider
    checkout handle when one exists.

    `errors` carries per-field detail (Cloudflare / Stripe shape)
    for 422 sites that emit a list of field-level failures — used
    today by the kind=validate edge rule so a JSON Schema
    rejection renders as a form-field list the dashboard can
    iterate without parsing prose. Optional + omitempty so every
    other problem+json site keeps its existing flat shape unchanged.
     """
    pre_cutover_health: ProjectRollbackPreCutoverHealth | Unset = UNSET
    """ Durable outcome of configured HTTP probes of the prepared revision before cutover. """
    health: ProjectRollbackHealth | Unset = UNSET
    """ Durable outcome of the configured HTTP health probes for the exact restored route. """
    live_traffic: ProjectRollbackLiveTraffic | Unset = UNSET
    """ Verification of configured 5xx budgets using production requests after an exact route generation was
    applied. """
    traffic: ProjectRollbackTraffic | Unset = UNSET
    """ Observed HTTP recovery for the exact restored deployment. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        service = self.service

        slug = self.slug

        status: str = self.status

        from_revision = self.from_revision

        to_revision = self.to_revision

        reason = self.reason

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        scope = self.scope

        expected_current_deployment_id: str | Unset = UNSET
        if not isinstance(self.expected_current_deployment_id, Unset):
            expected_current_deployment_id = str(self.expected_current_deployment_id)

        current_served_at: str | Unset = UNSET
        if not isinstance(self.current_served_at, Unset):
            current_served_at = self.current_served_at.isoformat()

        target_deployment_id: str | Unset = UNSET
        if not isinstance(self.target_deployment_id, Unset):
            target_deployment_id = str(self.target_deployment_id)

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        attempt_id: str | Unset = UNSET
        if not isinstance(self.attempt_id, Unset):
            attempt_id = str(self.attempt_id)

        rollback_operation_id: str | Unset = UNSET
        if not isinstance(self.rollback_operation_id, Unset):
            rollback_operation_id = str(self.rollback_operation_id)

        preparation_status = self.preparation_status

        blockers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blockers, Unset):
            blockers = []
            for blockers_item_data in self.blockers:
                blockers_item = blockers_item_data.to_dict()
                blockers.append(blockers_item)

        warnings: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.warnings, Unset):
            warnings = []
            for warnings_item_data in self.warnings:
                warnings_item = warnings_item_data.to_dict()
                warnings.append(warnings_item)

        crossed_releases: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.crossed_releases, Unset):
            crossed_releases = []
            for crossed_releases_item_data in self.crossed_releases:
                crossed_releases_item = crossed_releases_item_data.to_dict()
                crossed_releases.append(crossed_releases_item)

        undo_artifacts: dict[str, Any] | Unset = UNSET
        if not isinstance(self.undo_artifacts, Unset):
            undo_artifacts = self.undo_artifacts.to_dict()

        problem: dict[str, Any] | Unset = UNSET
        if not isinstance(self.problem, Unset):
            problem = self.problem.to_dict()

        pre_cutover_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.pre_cutover_health, Unset):
            pre_cutover_health = self.pre_cutover_health.to_dict()

        health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.health, Unset):
            health = self.health.to_dict()

        live_traffic: dict[str, Any] | Unset = UNSET
        if not isinstance(self.live_traffic, Unset):
            live_traffic = self.live_traffic.to_dict()

        traffic: dict[str, Any] | Unset = UNSET
        if not isinstance(self.traffic, Unset):
            traffic = self.traffic.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "service": service,
                "slug": slug,
                "status": status,
            }
        )
        if from_revision is not UNSET:
            field_dict["from_revision"] = from_revision
        if to_revision is not UNSET:
            field_dict["to_revision"] = to_revision
        if reason is not UNSET:
            field_dict["reason"] = reason
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if scope is not UNSET:
            field_dict["scope"] = scope
        if expected_current_deployment_id is not UNSET:
            field_dict["expected_current_deployment_id"] = expected_current_deployment_id
        if current_served_at is not UNSET:
            field_dict["current_served_at"] = current_served_at
        if target_deployment_id is not UNSET:
            field_dict["target_deployment_id"] = target_deployment_id
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if attempt_id is not UNSET:
            field_dict["attempt_id"] = attempt_id
        if rollback_operation_id is not UNSET:
            field_dict["rollback_operation_id"] = rollback_operation_id
        if preparation_status is not UNSET:
            field_dict["preparation_status"] = preparation_status
        if blockers is not UNSET:
            field_dict["blockers"] = blockers
        if warnings is not UNSET:
            field_dict["warnings"] = warnings
        if crossed_releases is not UNSET:
            field_dict["crossed_releases"] = crossed_releases
        if undo_artifacts is not UNSET:
            field_dict["undo_artifacts"] = undo_artifacts
        if problem is not UNSET:
            field_dict["problem"] = problem
        if pre_cutover_health is not UNSET:
            field_dict["pre_cutover_health"] = pre_cutover_health
        if health is not UNSET:
            field_dict["health"] = health
        if live_traffic is not UNSET:
            field_dict["live_traffic"] = live_traffic
        if traffic is not UNSET:
            field_dict["traffic"] = traffic

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_check_finding import BindingCheckFinding
        from ..models.problem import Problem
        from ..models.project_rollback_health import ProjectRollbackHealth
        from ..models.project_rollback_live_traffic import ProjectRollbackLiveTraffic
        from ..models.project_rollback_pre_cutover_health import ProjectRollbackPreCutoverHealth
        from ..models.project_rollback_traffic import ProjectRollbackTraffic
        from ..models.project_rollback_undo_artifacts import ProjectRollbackUndoArtifacts
        from ..models.rollback_crossed_release import RollbackCrossedRelease

        d = dict(src_dict)
        service = d.pop("service")

        slug = d.pop("slug")

        status = check_project_rollback_step_status(d.pop("status"))

        from_revision = d.pop("from_revision", UNSET)

        to_revision = d.pop("to_revision", UNSET)

        reason = d.pop("reason", UNSET)

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        scope = d.pop("scope", UNSET)

        _expected_current_deployment_id = d.pop("expected_current_deployment_id", UNSET)
        expected_current_deployment_id: UUID | Unset
        if isinstance(_expected_current_deployment_id, Unset):
            expected_current_deployment_id = UNSET
        else:
            expected_current_deployment_id = UUID(_expected_current_deployment_id)

        _current_served_at = d.pop("current_served_at", UNSET)
        current_served_at: datetime.datetime | Unset
        if isinstance(_current_served_at, Unset):
            current_served_at = UNSET
        else:
            current_served_at = datetime.datetime.fromisoformat(_current_served_at)

        _target_deployment_id = d.pop("target_deployment_id", UNSET)
        target_deployment_id: UUID | Unset
        if isinstance(_target_deployment_id, Unset):
            target_deployment_id = UNSET
        else:
            target_deployment_id = UUID(_target_deployment_id)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        _attempt_id = d.pop("attempt_id", UNSET)
        attempt_id: UUID | Unset
        if isinstance(_attempt_id, Unset):
            attempt_id = UNSET
        else:
            attempt_id = UUID(_attempt_id)

        _rollback_operation_id = d.pop("rollback_operation_id", UNSET)
        rollback_operation_id: UUID | Unset
        if isinstance(_rollback_operation_id, Unset):
            rollback_operation_id = UNSET
        else:
            rollback_operation_id = UUID(_rollback_operation_id)

        preparation_status = d.pop("preparation_status", UNSET)

        _blockers = d.pop("blockers", UNSET)
        blockers: list[BindingCheckFinding] | Unset = UNSET
        if _blockers is not UNSET:
            blockers = []
            for blockers_item_data in _blockers:
                blockers_item = BindingCheckFinding.from_dict(blockers_item_data)

                blockers.append(blockers_item)

        _warnings = d.pop("warnings", UNSET)
        warnings: list[BindingCheckFinding] | Unset = UNSET
        if _warnings is not UNSET:
            warnings = []
            for warnings_item_data in _warnings:
                warnings_item = BindingCheckFinding.from_dict(warnings_item_data)

                warnings.append(warnings_item)

        _crossed_releases = d.pop("crossed_releases", UNSET)
        crossed_releases: list[RollbackCrossedRelease] | Unset = UNSET
        if _crossed_releases is not UNSET:
            crossed_releases = []
            for crossed_releases_item_data in _crossed_releases:
                crossed_releases_item = RollbackCrossedRelease.from_dict(crossed_releases_item_data)

                crossed_releases.append(crossed_releases_item)

        _undo_artifacts = d.pop("undo_artifacts", UNSET)
        undo_artifacts: ProjectRollbackUndoArtifacts | Unset
        if isinstance(_undo_artifacts, Unset):
            undo_artifacts = UNSET
        else:
            undo_artifacts = ProjectRollbackUndoArtifacts.from_dict(_undo_artifacts)

        _problem = d.pop("problem", UNSET)
        problem: Problem | Unset
        if isinstance(_problem, Unset):
            problem = UNSET
        else:
            problem = Problem.from_dict(_problem)

        _pre_cutover_health = d.pop("pre_cutover_health", UNSET)
        pre_cutover_health: ProjectRollbackPreCutoverHealth | Unset
        if isinstance(_pre_cutover_health, Unset):
            pre_cutover_health = UNSET
        else:
            pre_cutover_health = ProjectRollbackPreCutoverHealth.from_dict(_pre_cutover_health)

        _health = d.pop("health", UNSET)
        health: ProjectRollbackHealth | Unset
        if isinstance(_health, Unset):
            health = UNSET
        else:
            health = ProjectRollbackHealth.from_dict(_health)

        _live_traffic = d.pop("live_traffic", UNSET)
        live_traffic: ProjectRollbackLiveTraffic | Unset
        if isinstance(_live_traffic, Unset):
            live_traffic = UNSET
        else:
            live_traffic = ProjectRollbackLiveTraffic.from_dict(_live_traffic)

        _traffic = d.pop("traffic", UNSET)
        traffic: ProjectRollbackTraffic | Unset
        if isinstance(_traffic, Unset):
            traffic = UNSET
        else:
            traffic = ProjectRollbackTraffic.from_dict(_traffic)

        project_rollback_step = cls(
            service=service,
            slug=slug,
            status=status,
            from_revision=from_revision,
            to_revision=to_revision,
            reason=reason,
            app_id=app_id,
            scope=scope,
            expected_current_deployment_id=expected_current_deployment_id,
            current_served_at=current_served_at,
            target_deployment_id=target_deployment_id,
            deployment_id=deployment_id,
            attempt_id=attempt_id,
            rollback_operation_id=rollback_operation_id,
            preparation_status=preparation_status,
            blockers=blockers,
            warnings=warnings,
            crossed_releases=crossed_releases,
            undo_artifacts=undo_artifacts,
            problem=problem,
            pre_cutover_health=pre_cutover_health,
            health=health,
            live_traffic=live_traffic,
            traffic=traffic,
        )

        project_rollback_step.additional_properties = d
        return project_rollback_step

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
