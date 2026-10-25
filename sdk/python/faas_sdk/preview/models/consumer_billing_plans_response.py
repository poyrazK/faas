from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.consumer_signup_plan import ConsumerSignupPlan


T = TypeVar("T", bound="ConsumerBillingPlansResponse")


@_attrs_define
class ConsumerBillingPlansResponse:
    """The plans a consumer can switch between (ADR-20261021183000)."""

    can_change: bool
    can_close: bool
    plans: list[ConsumerSignupPlan]
    current_plan_id: UUID | Unset = UNSET
    """Omitted on the app's default plan."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        can_change = self.can_change

        can_close = self.can_close

        plans = []
        for plans_item_data in self.plans:
            plans_item = plans_item_data.to_dict()
            plans.append(plans_item)

        current_plan_id: str | Unset = UNSET
        if not isinstance(self.current_plan_id, Unset):
            current_plan_id = str(self.current_plan_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "can_change": can_change,
                "can_close": can_close,
                "plans": plans,
            }
        )
        if current_plan_id is not UNSET:
            field_dict["current_plan_id"] = current_plan_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.consumer_signup_plan import ConsumerSignupPlan

        d = dict(src_dict)
        can_change = d.pop("can_change")

        can_close = d.pop("can_close")

        plans = []
        _plans = d.pop("plans")
        for plans_item_data in _plans:
            plans_item = ConsumerSignupPlan.from_dict(plans_item_data)

            plans.append(plans_item)

        _current_plan_id = d.pop("current_plan_id", UNSET)
        current_plan_id: UUID | Unset
        if isinstance(_current_plan_id, Unset):
            current_plan_id = UNSET
        else:
            current_plan_id = UUID(_current_plan_id)

        consumer_billing_plans_response = cls(
            can_change=can_change,
            can_close=can_close,
            plans=plans,
            current_plan_id=current_plan_id,
        )

        consumer_billing_plans_response.additional_properties = d
        return consumer_billing_plans_response

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
