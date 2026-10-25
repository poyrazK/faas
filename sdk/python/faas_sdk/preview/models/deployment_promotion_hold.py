from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_promotion_hold_last_recovery_outcome import (
    DeploymentPromotionHoldLastRecoveryOutcome,
    check_deployment_promotion_hold_last_recovery_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentPromotionHold")


@_attrs_define
class DeploymentPromotionHold:
    """Compatibility hold and durable recovery progress for a deployment awaiting promotion."""

    code: str
    detail: str
    observed_at: datetime.datetime
    held_at: datetime.datetime
    """First compatibility hold observation."""
    recovery_attempts: int
    """Number of claimed automatic recovery attempts."""
    recovery_in_progress: bool
    next_retry_at: datetime.datetime | Unset = UNSET
    """Eligibility time; while an attempt runs this is its lease expiry, not a promise of execution."""
    last_recovery_outcome: DeploymentPromotionHoldLastRecoveryOutcome | Unset = UNSET
    last_recovery_at: datetime.datetime | Unset = UNSET
    last_recovery_error_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        detail = self.detail

        observed_at = self.observed_at.isoformat()

        held_at = self.held_at.isoformat()

        recovery_attempts = self.recovery_attempts

        recovery_in_progress = self.recovery_in_progress

        next_retry_at: str | Unset = UNSET
        if not isinstance(self.next_retry_at, Unset):
            next_retry_at = self.next_retry_at.isoformat()

        last_recovery_outcome: str | Unset = UNSET
        if not isinstance(self.last_recovery_outcome, Unset):
            last_recovery_outcome = self.last_recovery_outcome

        last_recovery_at: str | Unset = UNSET
        if not isinstance(self.last_recovery_at, Unset):
            last_recovery_at = self.last_recovery_at.isoformat()

        last_recovery_error_code = self.last_recovery_error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "detail": detail,
                "observed_at": observed_at,
                "held_at": held_at,
                "recovery_attempts": recovery_attempts,
                "recovery_in_progress": recovery_in_progress,
            }
        )
        if next_retry_at is not UNSET:
            field_dict["next_retry_at"] = next_retry_at
        if last_recovery_outcome is not UNSET:
            field_dict["last_recovery_outcome"] = last_recovery_outcome
        if last_recovery_at is not UNSET:
            field_dict["last_recovery_at"] = last_recovery_at
        if last_recovery_error_code is not UNSET:
            field_dict["last_recovery_error_code"] = last_recovery_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        detail = d.pop("detail")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        held_at = datetime.datetime.fromisoformat(d.pop("held_at"))

        recovery_attempts = d.pop("recovery_attempts")

        recovery_in_progress = d.pop("recovery_in_progress")

        _next_retry_at = d.pop("next_retry_at", UNSET)
        next_retry_at: datetime.datetime | Unset
        if isinstance(_next_retry_at, Unset):
            next_retry_at = UNSET
        else:
            next_retry_at = datetime.datetime.fromisoformat(_next_retry_at)

        _last_recovery_outcome = d.pop("last_recovery_outcome", UNSET)
        last_recovery_outcome: DeploymentPromotionHoldLastRecoveryOutcome | Unset
        if isinstance(_last_recovery_outcome, Unset):
            last_recovery_outcome = UNSET
        else:
            last_recovery_outcome = check_deployment_promotion_hold_last_recovery_outcome(_last_recovery_outcome)

        _last_recovery_at = d.pop("last_recovery_at", UNSET)
        last_recovery_at: datetime.datetime | Unset
        if isinstance(_last_recovery_at, Unset):
            last_recovery_at = UNSET
        else:
            last_recovery_at = datetime.datetime.fromisoformat(_last_recovery_at)

        last_recovery_error_code = d.pop("last_recovery_error_code", UNSET)

        deployment_promotion_hold = cls(
            code=code,
            detail=detail,
            observed_at=observed_at,
            held_at=held_at,
            recovery_attempts=recovery_attempts,
            recovery_in_progress=recovery_in_progress,
            next_retry_at=next_retry_at,
            last_recovery_outcome=last_recovery_outcome,
            last_recovery_at=last_recovery_at,
            last_recovery_error_code=last_recovery_error_code,
        )

        deployment_promotion_hold.additional_properties = d
        return deployment_promotion_hold

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
