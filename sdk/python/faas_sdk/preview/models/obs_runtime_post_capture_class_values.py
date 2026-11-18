from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObsRuntimePostCaptureClassValues")


@_attrs_define
class ObsRuntimePostCaptureClassValues:
    """Durable snapshot-flow evidence in this replica view. Unknown age is included in pending. Known boundary timing is
    required only when pending exceeds unknown_age; otherwise both timing fields are absent. A boundary can be capture
    park time, init publication or retained gate/verification evidence and may precede the rollout. Age is not
    continuous residence in a class, an execution deadline or proof of deployment readiness. The unknown class requires
    pending equal to unknown_age. The integer ceiling is a wire bound, not an admission quota.

    """

    pending: int
    unknown_age: int
    oldest_boundary_at: datetime.datetime | Unset = UNSET
    oldest_known_age_seconds: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pending = self.pending

        unknown_age = self.unknown_age

        oldest_boundary_at: str | Unset = UNSET
        if not isinstance(self.oldest_boundary_at, Unset):
            oldest_boundary_at = self.oldest_boundary_at.isoformat()

        oldest_known_age_seconds = self.oldest_known_age_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pending": pending,
                "unknown_age": unknown_age,
            }
        )
        if oldest_boundary_at is not UNSET:
            field_dict["oldest_boundary_at"] = oldest_boundary_at
        if oldest_known_age_seconds is not UNSET:
            field_dict["oldest_known_age_seconds"] = oldest_known_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        pending = d.pop("pending")

        unknown_age = d.pop("unknown_age")

        _oldest_boundary_at = d.pop("oldest_boundary_at", UNSET)
        oldest_boundary_at: datetime.datetime | Unset
        if isinstance(_oldest_boundary_at, Unset):
            oldest_boundary_at = UNSET
        else:
            oldest_boundary_at = datetime.datetime.fromisoformat(_oldest_boundary_at)

        oldest_known_age_seconds = d.pop("oldest_known_age_seconds", UNSET)

        obs_runtime_post_capture_class_values = cls(
            pending=pending,
            unknown_age=unknown_age,
            oldest_boundary_at=oldest_boundary_at,
            oldest_known_age_seconds=oldest_known_age_seconds,
        )

        obs_runtime_post_capture_class_values.additional_properties = d
        return obs_runtime_post_capture_class_values

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
