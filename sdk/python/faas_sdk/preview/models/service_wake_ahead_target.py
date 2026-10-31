from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ServiceWakeAheadTarget")


@_attrs_define
class ServiceWakeAheadTarget:
    """One measured caller-to-service edge and its wake-ahead outcomes."""

    app_id: UUID
    slug: str
    call_share: float
    """Share of cold wakes followed by a call to this service."""
    cold_share: float
    """Share of those calls that found it parked or woken ahead."""
    qualifies: bool
    """The edge meets the wake-ahead thresholds."""
    started: int
    used: int
    unused: int
    failed: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        slug = self.slug

        call_share = self.call_share

        cold_share = self.cold_share

        qualifies = self.qualifies

        started = self.started

        used = self.used

        unused = self.unused

        failed = self.failed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "slug": slug,
                "call_share": call_share,
                "cold_share": cold_share,
                "qualifies": qualifies,
                "started": started,
                "used": used,
                "unused": unused,
                "failed": failed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        slug = d.pop("slug")

        call_share = d.pop("call_share")

        cold_share = d.pop("cold_share")

        qualifies = d.pop("qualifies")

        started = d.pop("started")

        used = d.pop("used")

        unused = d.pop("unused")

        failed = d.pop("failed")

        service_wake_ahead_target = cls(
            app_id=app_id,
            slug=slug,
            call_share=call_share,
            cold_share=cold_share,
            qualifies=qualifies,
            started=started,
            used=used,
            unused=unused,
            failed=failed,
        )

        service_wake_ahead_target.additional_properties = d
        return service_wake_ahead_target

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
