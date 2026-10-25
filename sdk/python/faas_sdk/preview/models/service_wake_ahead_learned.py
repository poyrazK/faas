from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.service_wake_ahead_learned_source import (
    ServiceWakeAheadLearnedSource,
    check_service_wake_ahead_learned_source,
)

if TYPE_CHECKING:
    from ..models.service_wake_ahead_target import ServiceWakeAheadTarget


T = TypeVar("T", bound="ServiceWakeAheadLearned")


@_attrs_define
class ServiceWakeAheadLearned:
    """What the gateways measured for this caller app (ADR-1152): its top service edges and what wake-ahead did over the
    evidence window. Present on GET; source is unavailable when gateway metrics cannot be read.

    """

    source: ServiceWakeAheadLearnedSource
    window: str
    """Window of the outcome counts, for example 24h."""
    caller_wakes: int
    """Cold wakes of this app the evidence covers (older wakes decay)."""
    targets: list[ServiceWakeAheadTarget]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        window = self.window

        caller_wakes = self.caller_wakes

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "window": window,
                "caller_wakes": caller_wakes,
                "targets": targets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.service_wake_ahead_target import ServiceWakeAheadTarget

        d = dict(src_dict)
        source = check_service_wake_ahead_learned_source(d.pop("source"))

        window = d.pop("window")

        caller_wakes = d.pop("caller_wakes")

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = ServiceWakeAheadTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        service_wake_ahead_learned = cls(
            source=source,
            window=window,
            caller_wakes=caller_wakes,
            targets=targets,
        )

        service_wake_ahead_learned.additional_properties = d
        return service_wake_ahead_learned

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
