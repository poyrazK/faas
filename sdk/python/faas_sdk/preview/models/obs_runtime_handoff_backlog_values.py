from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimeHandoffBacklogValues")


@_attrs_define
class ObsRuntimeHandoffBacklogValues:
    """Eligible succeeded builds without an image-preparation checkpoint in this instance's recovery scope. Oldest finish
    time can precede the rollout. Zero work omits that time; ages allow the existing clock skew and never imply a
    stalled worker or a ready deployment.

    """

    pending: int
    oldest_wait_seconds: float
    oldest_finished_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pending = self.pending

        oldest_wait_seconds = self.oldest_wait_seconds

        oldest_finished_at: str | Unset = UNSET
        if not isinstance(self.oldest_finished_at, Unset):
            oldest_finished_at = self.oldest_finished_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pending": pending,
                "oldest_wait_seconds": oldest_wait_seconds,
            }
        )
        if oldest_finished_at is not UNSET:
            field_dict["oldest_finished_at"] = oldest_finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pending = d.pop("pending")

        oldest_wait_seconds = d.pop("oldest_wait_seconds")

        _oldest_finished_at = d.pop("oldest_finished_at", UNSET)
        oldest_finished_at: datetime.datetime | Unset
        if isinstance(_oldest_finished_at, Unset):
            oldest_finished_at = UNSET
        else:
            oldest_finished_at = datetime.datetime.fromisoformat(_oldest_finished_at)

        obs_runtime_handoff_backlog_values = cls(
            pending=pending,
            oldest_wait_seconds=oldest_wait_seconds,
            oldest_finished_at=oldest_finished_at,
        )

        obs_runtime_handoff_backlog_values.additional_properties = d
        return obs_runtime_handoff_backlog_values

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
