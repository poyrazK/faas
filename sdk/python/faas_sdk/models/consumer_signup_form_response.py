from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.consumer_signup_plan import ConsumerSignupPlan


T = TypeVar("T", bound="ConsumerSignupFormResponse")


@_attrs_define
class ConsumerSignupFormResponse:
    """What the sign-up page shows."""

    app: str
    display_name: str
    livemode: bool
    """False while the owner's Stripe key is a test key."""
    plans: list[ConsumerSignupPlan]
    terms_url: str | Unset = UNSET
    trial_days: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        display_name = self.display_name

        livemode = self.livemode

        plans = []
        for plans_item_data in self.plans:
            plans_item = plans_item_data.to_dict()
            plans.append(plans_item)

        terms_url = self.terms_url

        trial_days = self.trial_days

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "display_name": display_name,
                "livemode": livemode,
                "plans": plans,
            }
        )
        if terms_url is not UNSET:
            field_dict["terms_url"] = terms_url
        if trial_days is not UNSET:
            field_dict["trial_days"] = trial_days

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

        terms_url = d.pop("terms_url", UNSET)

        trial_days = d.pop("trial_days", UNSET)

        consumer_signup_form_response = cls(
            app=app,
            display_name=display_name,
            livemode=livemode,
            plans=plans,
            terms_url=terms_url,
            trial_days=trial_days,
        )

        consumer_signup_form_response.additional_properties = d
        return consumer_signup_form_response

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
