from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowRetainedMilestoneFreshness")


@_attrs_define
class OperationWorkflowRetainedMilestoneFreshness:
    """Evidence age evaluated at the shared server timestamp. Expiry is inclusive; missing evidence omits expiry and age
    fields. Remaining seconds round up while eligible and are zero after expiry.

    """

    max_age_seconds: int
    evaluated_at: datetime.datetime
    expires_at: datetime.datetime | Unset = UNSET
    age_seconds: int | Unset = UNSET
    remaining_seconds: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        max_age_seconds = self.max_age_seconds

        evaluated_at = self.evaluated_at.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        age_seconds = self.age_seconds

        remaining_seconds = self.remaining_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "max_age_seconds": max_age_seconds,
                "evaluated_at": evaluated_at,
            }
        )
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if age_seconds is not UNSET:
            field_dict["age_seconds"] = age_seconds
        if remaining_seconds is not UNSET:
            field_dict["remaining_seconds"] = remaining_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_age_seconds = d.pop("max_age_seconds")

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        age_seconds = d.pop("age_seconds", UNSET)

        remaining_seconds = d.pop("remaining_seconds", UNSET)

        operation_workflow_retained_milestone_freshness = cls(
            max_age_seconds=max_age_seconds,
            evaluated_at=evaluated_at,
            expires_at=expires_at,
            age_seconds=age_seconds,
            remaining_seconds=remaining_seconds,
        )

        return operation_workflow_retained_milestone_freshness
