from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentPromotionHold")


@_attrs_define
class DeploymentPromotionHold:
    code: str
    detail: str
    observed_at: datetime.datetime
    held_at: datetime.datetime | Unset = UNSET
    recovery_attempts: int | Unset = UNSET
    next_retry_at: datetime.datetime | Unset = UNSET
    recovery_in_progress: bool | Unset = UNSET
    last_recovery_outcome: str | Unset = UNSET
    last_recovery_at: datetime.datetime | Unset = UNSET
    last_recovery_error_code: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        result: dict[str, Any] = {"code": self.code, "detail": self.detail, "observed_at": self.observed_at.isoformat()}
        for name in ("held_at", "recovery_attempts", "next_retry_at", "recovery_in_progress", "last_recovery_outcome", "last_recovery_at", "last_recovery_error_code"):
            value = getattr(self, name)
            if not isinstance(value, Unset):
                result[name] = value.isoformat() if isinstance(value, datetime.datetime) else value
        return result

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        fields: dict[str, Any] = {}
        for name in ("held_at", "next_retry_at", "last_recovery_at"):
            value = d.pop(name, UNSET)
            fields[name] = UNSET if isinstance(value, Unset) else datetime.datetime.fromisoformat(value)
        return cls(
            code=d.pop("code"), detail=d.pop("detail"), observed_at=datetime.datetime.fromisoformat(d.pop("observed_at")),
            recovery_attempts=d.pop("recovery_attempts", UNSET), recovery_in_progress=d.pop("recovery_in_progress", UNSET),
            last_recovery_outcome=d.pop("last_recovery_outcome", UNSET), last_recovery_error_code=d.pop("last_recovery_error_code", UNSET), **fields,
        )
