from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_verification_check import BindingVerificationCheck


T = TypeVar("T", bound="BindingVerification")


@_attrs_define
class BindingVerification:
    """Sanitized durable evidence from the latest admitted service, PostgreSQL or object-storage task guest canary. Object-
    storage verification checks bucket-list read access only; resident application adoption is not checked.

    """

    result: str
    """Outcome of the recorded canary: passed, failed or unknown. May describe stale evidence."""
    source: str
    """Currently task_guest."""
    deployment_id: UUID
    scope: str
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
