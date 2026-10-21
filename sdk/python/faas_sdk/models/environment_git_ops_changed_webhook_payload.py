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


T = TypeVar("T", bound="EnvironmentGitOpsChangedWebhookPayload")


@_attrs_define
class EnvironmentGitOpsChangedWebhookPayload:
    """Value-free account webhook emitted atomically with a current GitOps report or completed source poll. Unchanged
    status is silent; first failures and later recoveries are delivered to explicit environment.gitops.changed
    subscribers. The drift and source axes remain independent.

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
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if error_code is not UNSET:
            field_dict["error_code"] = error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_git_ops_change import EnvironmentGitOpsChange

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
