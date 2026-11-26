from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PutDraftBillingRunScheduleRequest")


@_attrs_define
class PutDraftBillingRunScheduleRequest:
    """Timing and optional reviewed version for a monthly draft schedule."""

    delay_hours: int | Unset = UNSET
    """Hours after month end when the schedule starts the run. Defaults to 24."""
    expected_updated_at: datetime.datetime | Unset = UNSET
    """The reviewed draft schedule version. Omit to create only if there is no schedule."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        delay_hours = self.delay_hours

        expected_updated_at: str | Unset = UNSET
        if not isinstance(self.expected_updated_at, Unset):
            expected_updated_at = self.expected_updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if delay_hours is not UNSET:
            field_dict["delay_hours"] = delay_hours
        if expected_updated_at is not UNSET:
            field_dict["expected_updated_at"] = expected_updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        delay_hours = d.pop("delay_hours", UNSET)

        _expected_updated_at = d.pop("expected_updated_at", UNSET)
        expected_updated_at: datetime.datetime | Unset
        if isinstance(_expected_updated_at, Unset):
            expected_updated_at = UNSET
        else:
            expected_updated_at = datetime.datetime.fromisoformat(_expected_updated_at)

        put_draft_billing_run_schedule_request = cls(
            delay_hours=delay_hours,
            expected_updated_at=expected_updated_at,
        )

        put_draft_billing_run_schedule_request.additional_properties = d
        return put_draft_billing_run_schedule_request

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
