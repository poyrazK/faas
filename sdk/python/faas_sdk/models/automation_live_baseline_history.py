from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_live_baseline import AutomationLiveBaseline


T = TypeVar("T", bound="AutomationLiveBaselineHistory")


@_attrs_define
class AutomationLiveBaselineHistory:
    promotions: list[AutomationLiveBaseline]

    def to_dict(self) -> dict[str, Any]:
        promotions = []
        for promotions_item_data in self.promotions:
            promotions_item = promotions_item_data.to_dict()
            promotions.append(promotions_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "promotions": promotions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_baseline import AutomationLiveBaseline

        d = dict(src_dict)
        promotions = []
        _promotions = d.pop("promotions")
        for promotions_item_data in _promotions:
            promotions_item = AutomationLiveBaseline.from_dict(promotions_item_data)

            promotions.append(promotions_item)

        automation_live_baseline_history = cls(
            promotions=promotions,
        )

        return automation_live_baseline_history
