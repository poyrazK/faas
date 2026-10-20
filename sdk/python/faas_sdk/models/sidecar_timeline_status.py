from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.sidecar_timeline_status_status import SidecarTimelineStatusStatus, check_sidecar_timeline_status_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="SidecarTimelineStatus")


@_attrs_define
class SidecarTimelineStatus:
    """Most recent health transition for a sidecar, if one has been recorded."""

    at: datetime.datetime
    """RFC 3339 UTC timestamp of the health transition."""
    status: SidecarTimelineStatusStatus
    """Closed sidecar health state emitted by guest-init."""
    reason: str | Unset = UNSET
    """Optional producer-supplied transition reason."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        at = self.at.isoformat()

        status: str = self.status

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "at": at,
                "status": status,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        at = datetime.datetime.fromisoformat(d.pop("at"))

        status = check_sidecar_timeline_status_status(d.pop("status"))

        reason = d.pop("reason", UNSET)

        sidecar_timeline_status = cls(
            at=at,
            status=status,
            reason=reason,
        )

        sidecar_timeline_status.additional_properties = d
        return sidecar_timeline_status

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
