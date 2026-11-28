from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.environment_git_ops_changed_webhook_payload_axis import (
    EnvironmentGitOpsChangedWebhookPayloadAxis,
    check_environment_git_ops_changed_webhook_payload_axis,
)
from ..models.environment_git_ops_changed_webhook_payload_previous_status import (
    EnvironmentGitOpsChangedWebhookPayloadPreviousStatus,
    check_environment_git_ops_changed_webhook_payload_previous_status,
)
from ..models.environment_git_ops_changed_webhook_payload_status import (
    EnvironmentGitOpsChangedWebhookPayloadStatus,
    check_environment_git_ops_changed_webhook_payload_status,
)
from ..models.environment_git_ops_changed_webhook_payload_version import (
    EnvironmentGitOpsChangedWebhookPayloadVersion,
    check_environment_git_ops_changed_webhook_payload_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_git_ops_change import EnvironmentGitOpsChange
    from ..models.environment_git_ops_diagnostic import EnvironmentGitOpsDiagnostic


T = TypeVar("T", bound="EnvironmentGitOpsChangedWebhookPayload")


@_attrs_define
class EnvironmentGitOpsChangedWebhookPayload:
    """Value-free account webhook for independent drift, source and rollout axes. Rollout serving and failure outcomes are
    assessed from current exact-commit graph evidence when a current report completes; approval and control transactions
    supersede pending rollouts. Each terminal outcome is delivered once per approval generation. Pending observations
    stay silent. Existing drift/source transition rules and explicit environment.gitops.changed subscriptions apply.
    Delivery is signed and at least once; deduplicate retries by transition_id.

    """

    version: EnvironmentGitOpsChangedWebhookPayloadVersion
    transition_id: UUID
    source_id: UUID
    project: str
    environment: str
    axis: EnvironmentGitOpsChangedWebhookPayloadAxis
    previous_status: EnvironmentGitOpsChangedWebhookPayloadPreviousStatus
    status: EnvironmentGitOpsChangedWebhookPayloadStatus
    generation: int
    observed_version: int
    observed_at: datetime.datetime
    dashboard_path: str
    changes: list[EnvironmentGitOpsChange]
    blocking_reasons: list[str]
    details_truncated: bool
    approved_revision_id: UUID | Unset = UNSET
    commit_sha: str | Unset = UNSET
    definition_digest: str | Unset = UNSET
    graph_id: UUID | Unset = UNSET
    plan_hash: str | Unset = UNSET
    diagnostics: list[EnvironmentGitOpsDiagnostic] | Unset = UNSET
    """Safe mapped blocker codes and guidance, optionally scoped to a workload. Never includes upstream error text,
    logs, values or secret-reference targets."""
    run_id: UUID | Unset = UNSET
    error_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        transition_id = str(self.transition_id)

        source_id = str(self.source_id)

        project = self.project

        environment = self.environment

        axis: str = self.axis

        previous_status: str = self.previous_status

        status: str = self.status

        generation = self.generation

        observed_version = self.observed_version

        observed_at = self.observed_at.isoformat()

        dashboard_path = self.dashboard_path

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        blocking_reasons = self.blocking_reasons

        details_truncated = self.details_truncated

        approved_revision_id: str | Unset = UNSET
        if not isinstance(self.approved_revision_id, Unset):
            approved_revision_id = str(self.approved_revision_id)

        commit_sha = self.commit_sha

        definition_digest = self.definition_digest

        graph_id: str | Unset = UNSET
        if not isinstance(self.graph_id, Unset):
            graph_id = str(self.graph_id)

        plan_hash = self.plan_hash

        diagnostics: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.diagnostics, Unset):
            diagnostics = []
            for diagnostics_item_data in self.diagnostics:
                diagnostics_item = diagnostics_item_data.to_dict()
                diagnostics.append(diagnostics_item)

        run_id: str | Unset = UNSET
        if not isinstance(self.run_id, Unset):
            run_id = str(self.run_id)

        error_code = self.error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "transition_id": transition_id,
                "source_id": source_id,
                "project": project,
                "environment": environment,
                "axis": axis,
                "previous_status": previous_status,
                "status": status,
                "generation": generation,
                "observed_version": observed_version,
                "observed_at": observed_at,
                "dashboard_path": dashboard_path,
                "changes": changes,
                "blocking_reasons": blocking_reasons,
                "details_truncated": details_truncated,
            }
        )
        if approved_revision_id is not UNSET:
            field_dict["approved_revision_id"] = approved_revision_id
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if definition_digest is not UNSET:
            field_dict["definition_digest"] = definition_digest
        if graph_id is not UNSET:
            field_dict["graph_id"] = graph_id
        if plan_hash is not UNSET:
            field_dict["plan_hash"] = plan_hash
        if diagnostics is not UNSET:
            field_dict["diagnostics"] = diagnostics
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if error_code is not UNSET:
            field_dict["error_code"] = error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_ops_change import EnvironmentGitOpsChange
        from ..models.environment_git_ops_diagnostic import EnvironmentGitOpsDiagnostic

        d = dict(src_dict)
        version = check_environment_git_ops_changed_webhook_payload_version(d.pop("version"))

        transition_id = UUID(d.pop("transition_id"))

        source_id = UUID(d.pop("source_id"))

        project = d.pop("project")

        environment = d.pop("environment")

        axis = check_environment_git_ops_changed_webhook_payload_axis(d.pop("axis"))

        previous_status = check_environment_git_ops_changed_webhook_payload_previous_status(d.pop("previous_status"))

        status = check_environment_git_ops_changed_webhook_payload_status(d.pop("status"))

        generation = d.pop("generation")

        observed_version = d.pop("observed_version")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        dashboard_path = d.pop("dashboard_path")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = EnvironmentGitOpsChange.from_dict(changes_item_data)

            changes.append(changes_item)

        blocking_reasons = cast(list[str], d.pop("blocking_reasons"))

        details_truncated = d.pop("details_truncated")

        _approved_revision_id = d.pop("approved_revision_id", UNSET)
        approved_revision_id: UUID | Unset
        if isinstance(_approved_revision_id, Unset):
            approved_revision_id = UNSET
        else:
            approved_revision_id = UUID(_approved_revision_id)

        commit_sha = d.pop("commit_sha", UNSET)

        definition_digest = d.pop("definition_digest", UNSET)

        _graph_id = d.pop("graph_id", UNSET)
        graph_id: UUID | Unset
        if isinstance(_graph_id, Unset):
            graph_id = UNSET
        else:
            graph_id = UUID(_graph_id)

        plan_hash = d.pop("plan_hash", UNSET)

        _diagnostics = d.pop("diagnostics", UNSET)
        diagnostics: list[EnvironmentGitOpsDiagnostic] | Unset = UNSET
        if _diagnostics is not UNSET:
            diagnostics = []
            for diagnostics_item_data in _diagnostics:
                diagnostics_item = EnvironmentGitOpsDiagnostic.from_dict(diagnostics_item_data)

                diagnostics.append(diagnostics_item)

        _run_id = d.pop("run_id", UNSET)
        run_id: UUID | Unset
        if isinstance(_run_id, Unset):
            run_id = UNSET
        else:
            run_id = UUID(_run_id)

        error_code = d.pop("error_code", UNSET)

        environment_git_ops_changed_webhook_payload = cls(
            version=version,
            transition_id=transition_id,
            source_id=source_id,
            project=project,
            environment=environment,
            axis=axis,
            previous_status=previous_status,
            status=status,
            generation=generation,
            observed_version=observed_version,
            observed_at=observed_at,
            dashboard_path=dashboard_path,
            changes=changes,
            blocking_reasons=blocking_reasons,
            details_truncated=details_truncated,
            approved_revision_id=approved_revision_id,
            commit_sha=commit_sha,
            definition_digest=definition_digest,
            graph_id=graph_id,
            plan_hash=plan_hash,
            diagnostics=diagnostics,
            run_id=run_id,
            error_code=error_code,
        )

        environment_git_ops_changed_webhook_payload.additional_properties = d
        return environment_git_ops_changed_webhook_payload

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
