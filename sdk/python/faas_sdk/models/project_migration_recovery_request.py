from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="ProjectMigrationRecoveryRequest")


@_attrs_define
class ProjectMigrationRecoveryRequest:
    """Operator attestation after database inspection and cleanup execution quiescence, bound to a different verified
    active replacement. This request records recovery and retires drained old code; it does not execute SQL or replay
    cleanup.

    """

    request_id: str
    expected_task_id: UUID
    expected_generation: int
    replacement_attempt_id: str
    database_state_checked: bool
    """Operator must attest that database state was inspected and repaired."""
    execution_quiescent: bool
    """Operator must confirm the original cleanup execution has stopped."""
    evidence_hash: str

    def to_dict(self) -> dict[str, Any]:
        request_id = self.request_id

        expected_task_id = str(self.expected_task_id)

        expected_generation = self.expected_generation

        replacement_attempt_id = self.replacement_attempt_id

        database_state_checked = self.database_state_checked

        execution_quiescent = self.execution_quiescent

        evidence_hash = self.evidence_hash

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "expected_task_id": expected_task_id,
                "expected_generation": expected_generation,
                "replacement_attempt_id": replacement_attempt_id,
                "database_state_checked": database_state_checked,
                "execution_quiescent": execution_quiescent,
                "evidence_hash": evidence_hash,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = d.pop("request_id")

        expected_task_id = UUID(d.pop("expected_task_id"))

        expected_generation = d.pop("expected_generation")

        replacement_attempt_id = d.pop("replacement_attempt_id")

        database_state_checked = d.pop("database_state_checked")

        execution_quiescent = d.pop("execution_quiescent")

        evidence_hash = d.pop("evidence_hash")

        project_migration_recovery_request = cls(
            request_id=request_id,
            expected_task_id=expected_task_id,
            expected_generation=expected_generation,
            replacement_attempt_id=replacement_attempt_id,
            database_state_checked=database_state_checked,
            execution_quiescent=execution_quiescent,
            evidence_hash=evidence_hash,
        )

        return project_migration_recovery_request
