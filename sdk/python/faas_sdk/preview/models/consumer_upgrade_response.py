from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.consumer_signup_plan import ConsumerSignupPlan


T = TypeVar("T", bound="ConsumerUpgradeResponse")


@_attrs_define
class ConsumerUpgradeResponse:
    """What the upgrade page shows an upgrade link's customer."""

    app: str
    display_name: str
    livemode: bool
    plans: list[ConsumerSignupPlan]
    has_card: bool
    """A card is saved: a paid plan needs no checkout."""
    current_plan_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        display_name = self.display_name

        livemode = self.livemode

        plans = []
        for plans_item_data in self.plans:
            plans_item = plans_item_data.to_dict()
            plans.append(plans_item)

        has_card = self.has_card

        current_plan_id: str | Unset = UNSET
        if not isinstance(self.current_plan_id, Unset):
            current_plan_id = str(self.current_plan_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "display_name": display_name,
                "livemode": livemode,
                "plans": plans,
                "has_card": has_card,
            }
        )
        if current_plan_id is not UNSET:
            field_dict["current_plan_id"] = current_plan_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.consumer_signup_plan import ConsumerSignupPlan

        d = dict(src_dict)
        app = d.pop("app")

        display_name = d.pop("display_name")

        livemode = d.pop("livemode")

        plans = []
        _plans = d.pop("plans")
        for plans_item_data in _plans:
            plans_item = ConsumerSignupPlan.from_dict(plans_item_data)

            plans.append(plans_item)

        has_card = d.pop("has_card")

        _current_plan_id = d.pop("current_plan_id", UNSET)
        current_plan_id: UUID | Unset
        if isinstance(_current_plan_id, Unset):
            current_plan_id = UNSET
        else:
            current_plan_id = UUID(_current_plan_id)

        consumer_upgrade_response = cls(
            app=app,
            display_name=display_name,
            livemode=livemode,
            plans=plans,
            has_card=has_card,
            current_plan_id=current_plan_id,
        )

        consumer_upgrade_response.additional_properties = d
        return consumer_upgrade_response

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
