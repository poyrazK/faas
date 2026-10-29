from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.debug_request_correlation_stage import DebugRequestCorrelationStage


T = TypeVar("T", bound="DebugRequestCorrelation")


@_attrs_define
class DebugRequestCorrelation:
    """Fixed-shape edge-to-billing correlation for a retained request. Every stage is present so unavailable telemetry is
    visible.

    """

    stages: list[DebugRequestCorrelationStage]
    complete: bool
    """True only when every applicable stage has complete retained evidence."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        stages = []
        for stages_item_data in self.stages:
            stages_item = stages_item_data.to_dict()
            stages.append(stages_item)

        complete = self.complete

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "stages": stages,
                "complete": complete,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_request_correlation_stage import DebugRequestCorrelationStage

        d = dict(src_dict)
        stages = []
        _stages = d.pop("stages")
        for stages_item_data in _stages:
            stages_item = DebugRequestCorrelationStage.from_dict(stages_item_data)

            stages.append(stages_item)

        complete = d.pop("complete")

        debug_request_correlation = cls(
            stages=stages,
            complete=complete,
        )

        debug_request_correlation.additional_properties = d
        return debug_request_correlation

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
