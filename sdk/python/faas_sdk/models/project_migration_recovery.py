from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_migration_recovery_status import (
    ProjectMigrationRecoveryStatus,
    check_project_migration_recovery_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectMigrationRecovery")


@_attrs_define
class ProjectMigrationRecovery:
    """Durable operator resolution and verified roll-forward identity. The cleanup outcome remains unchanged; database and
    execution checks are operator attestations.

    """

    status: ProjectMigrationRecoveryStatus
    reason: str | Unset = UNSET
    request_id: str | Unset = UNSET
    task_id: UUID | Unset = UNSET
    expected_generation: int | Unset = UNSET
    replacement_attempt_id: str | Unset = UNSET
    replacement_release_id: UUID | Unset = UNSET
    evidence_hash: str | Unset = UNSET
    operator_account_id: UUID | Unset = UNSET
    resolved_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        reason = self.reason

        request_id = self.request_id

        task_id: str | Unset = UNSET
        if not isinstance(self.task_id, Unset):
            task_id = str(self.task_id)

        expected_generation = self.expected_generation

        replacement_attempt_id = self.replacement_attempt_id

        replacement_release_id: str | Unset = UNSET
        if not isinstance(self.replacement_release_id, Unset):
            replacement_release_id = str(self.replacement_release_id)

        evidence_hash = self.evidence_hash

        operator_account_id: str | Unset = UNSET
        if not isinstance(self.operator_account_id, Unset):
            operator_account_id = str(self.operator_account_id)

        resolved_at: str | Unset = UNSET
        if not isinstance(self.resolved_at, Unset):
            resolved_at = self.resolved_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if task_id is not UNSET:
            field_dict["task_id"] = task_id
        if expected_generation is not UNSET:
            field_dict["expected_generation"] = expected_generation
        if replacement_attempt_id is not UNSET:
            field_dict["replacement_attempt_id"] = replacement_attempt_id
        if replacement_release_id is not UNSET:
            field_dict["replacement_release_id"] = replacement_release_id
        if evidence_hash is not UNSET:
            field_dict["evidence_hash"] = evidence_hash
        if operator_account_id is not UNSET:
            field_dict["operator_account_id"] = operator_account_id
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_project_migration_recovery_status(d.pop("status"))

        reason = d.pop("reason", UNSET)

        request_id = d.pop("request_id", UNSET)

        _task_id = d.pop("task_id", UNSET)
        task_id: UUID | Unset
        if isinstance(_task_id, Unset):
            task_id = UNSET
        else:
            task_id = UUID(_task_id)

        expected_generation = d.pop("expected_generation", UNSET)

        replacement_attempt_id = d.pop("replacement_attempt_id", UNSET)

        _replacement_release_id = d.pop("replacement_release_id", UNSET)
        replacement_release_id: UUID | Unset
        if isinstance(_replacement_release_id, Unset):
            replacement_release_id = UNSET
        else:
            replacement_release_id = UUID(_replacement_release_id)

        evidence_hash = d.pop("evidence_hash", UNSET)

        _operator_account_id = d.pop("operator_account_id", UNSET)
        operator_account_id: UUID | Unset
        if isinstance(_operator_account_id, Unset):
            operator_account_id = UNSET
        else:
            operator_account_id = UUID(_operator_account_id)

        _resolved_at = d.pop("resolved_at", UNSET)
        resolved_at: datetime.datetime | Unset
        if isinstance(_resolved_at, Unset):
            resolved_at = UNSET
        else:
            resolved_at = datetime.datetime.fromisoformat(_resolved_at)

        project_migration_recovery = cls(
            status=status,
            reason=reason,
            request_id=request_id,
            task_id=task_id,
            expected_generation=expected_generation,
            replacement_attempt_id=replacement_attempt_id,
            replacement_release_id=replacement_release_id,
            evidence_hash=evidence_hash,
            operator_account_id=operator_account_id,
            resolved_at=resolved_at,
        )

        project_migration_recovery.additional_properties = d
        return project_migration_recovery

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
