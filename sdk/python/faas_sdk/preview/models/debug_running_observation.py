from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_running_cause import DebugRunningCause


T = TypeVar("T", bound="DebugRunningObservation")


@_attrs_define
class DebugRunningObservation:
    """Bounded point-in-time scheduler observation for the running debugger."""

    observed_at: datetime.datetime
    running_instances: int
    configured_min_instances: int
    effective_min_instances: int
    idle_timeout_seconds: int
    causes: list[DebugRunningCause]
    event_id: str | Unset = UNSET
    prewarm_min_instances: int | Unset = UNSET
    degraded: bool | Unset = UNSET
    """True when one or more scheduler signals were unavailable."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        running_instances = self.running_instances

        configured_min_instances = self.configured_min_instances

        effective_min_instances = self.effective_min_instances

        idle_timeout_seconds = self.idle_timeout_seconds

        causes = []
        for causes_item_data in self.causes:
            causes_item = causes_item_data.to_dict()
            causes.append(causes_item)

        event_id = self.event_id

        prewarm_min_instances = self.prewarm_min_instances

        degraded = self.degraded

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed_at": observed_at,
                "running_instances": running_instances,
                "configured_min_instances": configured_min_instances,
                "effective_min_instances": effective_min_instances,
                "idle_timeout_seconds": idle_timeout_seconds,
                "causes": causes,
            }
        )
        if event_id is not UNSET:
            field_dict["event_id"] = event_id
        if prewarm_min_instances is not UNSET:
            field_dict["prewarm_min_instances"] = prewarm_min_instances
        if degraded is not UNSET:
            field_dict["degraded"] = degraded

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_running_cause import DebugRunningCause

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        running_instances = d.pop("running_instances")

        configured_min_instances = d.pop("configured_min_instances")

        effective_min_instances = d.pop("effective_min_instances")

        idle_timeout_seconds = d.pop("idle_timeout_seconds")

        causes = []
        _causes = d.pop("causes")
        for causes_item_data in _causes:
            causes_item = DebugRunningCause.from_dict(causes_item_data)

            causes.append(causes_item)

        event_id = d.pop("event_id", UNSET)

        prewarm_min_instances = d.pop("prewarm_min_instances", UNSET)

        degraded = d.pop("degraded", UNSET)

        debug_running_observation = cls(
            observed_at=observed_at,
            running_instances=running_instances,
            configured_min_instances=configured_min_instances,
            effective_min_instances=effective_min_instances,
            idle_timeout_seconds=idle_timeout_seconds,
            causes=causes,
            event_id=event_id,
            prewarm_min_instances=prewarm_min_instances,
            degraded=degraded,
        )

        debug_running_observation.additional_properties = d
        return debug_running_observation

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
