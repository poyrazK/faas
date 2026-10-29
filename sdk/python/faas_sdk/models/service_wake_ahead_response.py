from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.service_wake_ahead_learned import ServiceWakeAheadLearned


T = TypeVar("T", bound="ServiceWakeAheadResponse")


@_attrs_define
class ServiceWakeAheadResponse:
    """An app's service wake-ahead opt-in."""

    slug: str
    enabled: bool
    """Whether a cold wake of this app also wakes the services it declares or is measured to call."""
    updated_at: datetime.datetime | Unset = UNSET
    """Omitted until the opt-in is first changed."""
    learned: ServiceWakeAheadLearned | Unset = UNSET
    """What the gateways measured for this caller app: its top service edges and what wake-ahead did over the
    evidence window. Present on GET; source is unavailable when gateway metrics cannot be read."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        enabled = self.enabled

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        learned: dict[str, Any] | Unset = UNSET
        if not isinstance(self.learned, Unset):
            learned = self.learned.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "enabled": enabled,
            }
        )
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at
        if learned is not UNSET:
            field_dict["learned"] = learned

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.service_wake_ahead_learned import ServiceWakeAheadLearned

        d = dict(src_dict)
        slug = d.pop("slug")

        enabled = d.pop("enabled")

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        _learned = d.pop("learned", UNSET)
        learned: ServiceWakeAheadLearned | Unset
        if isinstance(_learned, Unset):
            learned = UNSET
        else:
            learned = ServiceWakeAheadLearned.from_dict(_learned)

        service_wake_ahead_response = cls(
            slug=slug,
            enabled=enabled,
            updated_at=updated_at,
            learned=learned,
        )

        service_wake_ahead_response.additional_properties = d
        return service_wake_ahead_response

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
