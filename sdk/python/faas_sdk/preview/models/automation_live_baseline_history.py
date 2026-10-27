from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_live_baseline import AutomationLiveBaseline
    from ..models.automation_live_baseline_retirement import AutomationLiveBaselineRetirement


T = TypeVar("T", bound="AutomationLiveBaselineHistory")


@_attrs_define
class AutomationLiveBaselineHistory:
    """Bounded promotion audit history and optional permanent retirement receipt for one baseline name."""

    promotions: list[AutomationLiveBaseline]
    retirement: AutomationLiveBaselineRetirement | Unset = UNSET
    """Immutable attributed receipt recording permanent retirement and release of the baseline evidence pin."""

    def to_dict(self) -> dict[str, Any]:
        promotions = []
        for promotions_item_data in self.promotions:
            promotions_item = promotions_item_data.to_dict()
            promotions.append(promotions_item)

        retirement: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retirement, Unset):
            retirement = self.retirement.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "promotions": promotions,
            }
        )
        if retirement is not UNSET:
            field_dict["retirement"] = retirement

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_baseline import AutomationLiveBaseline
        from ..models.automation_live_baseline_retirement import AutomationLiveBaselineRetirement

        d = dict(src_dict)
        promotions = []
        _promotions = d.pop("promotions")
        for promotions_item_data in _promotions:
            promotions_item = AutomationLiveBaseline.from_dict(promotions_item_data)

            promotions.append(promotions_item)

        _retirement = d.pop("retirement", UNSET)
        retirement: AutomationLiveBaselineRetirement | Unset
        if isinstance(_retirement, Unset):
            retirement = UNSET
        else:
            retirement = AutomationLiveBaselineRetirement.from_dict(_retirement)

        automation_live_baseline_history = cls(
            promotions=promotions,
            retirement=retirement,
        )

        return automation_live_baseline_history
