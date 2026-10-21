from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.project_promotion_migration_recovery_request_action import (
    ProjectPromotionMigrationRecoveryRequestAction,
    check_project_promotion_migration_recovery_request_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectPromotionMigrationRecoveryRequest")


@_attrs_define
class ProjectPromotionMigrationRecoveryRequest:
    """Operator inspection evidence and captured task identity for resuming or resolving a promotion migration incident."""

    request_id: str
    expected_task_id: UUID
    expected_generation: int
    action: ProjectPromotionMigrationRecoveryRequestAction
    database_state_checked: bool
    """Explicit operator attestation of destination database inspection."""
    schema_compatible: bool
    """Required true for resume; attests that the expected expansion is present and backward-compatible."""
    evidence_hash: str
    """SHA256 reference to private inspection evidence. Contents are not uploaded."""
    replacement_promotion_id: UUID | Unset = UNSET
    """Required for resolve; must identify a different succeeded verified active promotion in the same destination."""

    def to_dict(self) -> dict[str, Any]:
        request_id = self.request_id

        expected_task_id = str(self.expected_task_id)

        expected_generation = self.expected_generation

        action: str = self.action

        database_state_checked = self.database_state_checked

        schema_compatible = self.schema_compatible

        evidence_hash = self.evidence_hash

        replacement_promotion_id: str | Unset = UNSET
        if not isinstance(self.replacement_promotion_id, Unset):
            replacement_promotion_id = str(self.replacement_promotion_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "request_id": request_id,
                "expected_task_id": expected_task_id,
                "expected_generation": expected_generation,
                "action": action,
                "database_state_checked": database_state_checked,
                "schema_compatible": schema_compatible,
                "evidence_hash": evidence_hash,
            }
        )
        if replacement_promotion_id is not UNSET:
            field_dict["replacement_promotion_id"] = replacement_promotion_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_id = d.pop("request_id")

        expected_task_id = UUID(d.pop("expected_task_id"))

        expected_generation = d.pop("expected_generation")

        action = check_project_promotion_migration_recovery_request_action(d.pop("action"))

        database_state_checked = d.pop("database_state_checked")

        schema_compatible = d.pop("schema_compatible")

        evidence_hash = d.pop("evidence_hash")

        _replacement_promotion_id = d.pop("replacement_promotion_id", UNSET)
        replacement_promotion_id: UUID | Unset
        if isinstance(_replacement_promotion_id, Unset):
            replacement_promotion_id = UNSET
        else:
            replacement_promotion_id = UUID(_replacement_promotion_id)

        project_promotion_migration_recovery_request = cls(
            request_id=request_id,
            expected_task_id=expected_task_id,
            expected_generation=expected_generation,
            action=action,
            database_state_checked=database_state_checked,
            schema_compatible=schema_compatible,
            evidence_hash=evidence_hash,
            replacement_promotion_id=replacement_promotion_id,
        )

        return project_promotion_migration_recovery_request
