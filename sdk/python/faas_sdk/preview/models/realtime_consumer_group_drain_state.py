from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeConsumerGroupDrainState")


@_attrs_define
class RealtimeConsumerGroupDrainState:
    """Realtime Consumer Group Drain State."""

    draining: bool
    drained: bool
    active_leases: int
    paused_at: datetime.datetime | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        draining = self.draining

        drained = self.drained

        active_leases = self.active_leases

        paused_at: None | str
        if isinstance(self.paused_at, datetime.datetime):
            paused_at = self.paused_at.isoformat()
        else:
            paused_at = self.paused_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "draining": draining,
                "drained": drained,
                "active_leases": active_leases,
                "paused_at": paused_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        draining = d.pop("draining")

        drained = d.pop("drained")

        active_leases = d.pop("active_leases")

        def _parse_paused_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                paused_at_type_0 = datetime.datetime.fromisoformat(data)

                return paused_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        paused_at = _parse_paused_at(d.pop("paused_at"))

        realtime_consumer_group_drain_state = cls(
            draining=draining,
            drained=drained,
            active_leases=active_leases,
            paused_at=paused_at,
        )

        realtime_consumer_group_drain_state.additional_properties = d
        return realtime_consumer_group_drain_state

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
