from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_verification_object_storage_mode import (
    BindingVerificationObjectStorageMode,
    check_binding_verification_object_storage_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_verification_check import BindingVerificationCheck


T = TypeVar("T", bound="BindingVerification")


@_attrs_define
class BindingVerification:
    """Sanitized durable evidence from the latest admitted service, PostgreSQL or object-storage task guest canary. Object-
    storage verification records bucket listing or explicitly requested permission-aware object operations and cleanup;
    inspect checks for coverage. Resident application adoption is not checked.

    """

    result: str
    """Outcome of the recorded canary: passed, failed or unknown. May describe stale evidence."""
    source: str
    """Currently task_guest."""
    deployment_id: UUID
    scope: str
    task_id: UUID | Unset = UNSET
    """Original probe task identity for pending observation and durable receipts."""
    target_deployment_id: UUID | Unset = UNSET
    """Service recipient qualified by the probe; absent for legacy any-replica evidence."""
    object_storage_mode: BindingVerificationObjectStorageMode | Unset = UNSET
    """Object operation level admitted for this stored verification; missing values denote a legacy listing canary
    and do not qualify required write operations."""
    reason: str | Unset = UNSET
    """Stable reason such as configuration_changed, deployment_changed, probe_pending, report_invalid or
    report_truncated."""
    checked_at: datetime.datetime | Unset = UNSET
    credential_generation: int | Unset = UNSET
    checks: list[BindingVerificationCheck] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        result = self.result

        source = self.source

        deployment_id = str(self.deployment_id)

        scope = self.scope

        task_id: str | Unset = UNSET
        if not isinstance(self.task_id, Unset):
            task_id = str(self.task_id)

        target_deployment_id: str | Unset = UNSET
        if not isinstance(self.target_deployment_id, Unset):
            target_deployment_id = str(self.target_deployment_id)

        object_storage_mode: str | Unset = UNSET
        if not isinstance(self.object_storage_mode, Unset):
            object_storage_mode = self.object_storage_mode

        reason = self.reason

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        credential_generation = self.credential_generation

        checks: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.checks, Unset):
            checks = []
            for checks_item_data in self.checks:
                checks_item = checks_item_data.to_dict()
                checks.append(checks_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "result": result,
                "source": source,
                "deployment_id": deployment_id,
                "scope": scope,
            }
        )
        if task_id is not UNSET:
            field_dict["task_id"] = task_id
        if target_deployment_id is not UNSET:
            field_dict["target_deployment_id"] = target_deployment_id
        if object_storage_mode is not UNSET:
            field_dict["object_storage_mode"] = object_storage_mode
        if reason is not UNSET:
            field_dict["reason"] = reason
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if credential_generation is not UNSET:
            field_dict["credential_generation"] = credential_generation
        if checks is not UNSET:
            field_dict["checks"] = checks

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_verification_check import BindingVerificationCheck

        d = dict(src_dict)
        result = d.pop("result")

        source = d.pop("source")

        deployment_id = UUID(d.pop("deployment_id"))

        scope = d.pop("scope")

        _task_id = d.pop("task_id", UNSET)
        task_id: UUID | Unset
        if isinstance(_task_id, Unset):
            task_id = UNSET
        else:
            task_id = UUID(_task_id)

        _target_deployment_id = d.pop("target_deployment_id", UNSET)
        target_deployment_id: UUID | Unset
        if isinstance(_target_deployment_id, Unset):
            target_deployment_id = UNSET
        else:
            target_deployment_id = UUID(_target_deployment_id)

        _object_storage_mode = d.pop("object_storage_mode", UNSET)
        object_storage_mode: BindingVerificationObjectStorageMode | Unset
        if isinstance(_object_storage_mode, Unset):
            object_storage_mode = UNSET
        else:
            object_storage_mode = check_binding_verification_object_storage_mode(_object_storage_mode)

        reason = d.pop("reason", UNSET)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        credential_generation = d.pop("credential_generation", UNSET)

        _checks = d.pop("checks", UNSET)
        checks: list[BindingVerificationCheck] | Unset = UNSET
        if _checks is not UNSET:
            checks = []
            for checks_item_data in _checks:
                checks_item = BindingVerificationCheck.from_dict(checks_item_data)

                checks.append(checks_item)

        binding_verification = cls(
            result=result,
            source=source,
            deployment_id=deployment_id,
            scope=scope,
            task_id=task_id,
            target_deployment_id=target_deployment_id,
            object_storage_mode=object_storage_mode,
            reason=reason,
            checked_at=checked_at,
            credential_generation=credential_generation,
            checks=checks,
        )

        binding_verification.additional_properties = d
        return binding_verification

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
