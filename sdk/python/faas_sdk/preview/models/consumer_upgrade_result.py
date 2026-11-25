from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ConsumerUpgradeResult")


@_attrs_define
class ConsumerUpgradeResult:
    """A changed plan, or the Stripe Checkout to save a card first."""

    plan_id: UUID
    effective_from: datetime.datetime | Unset = UNSET
    checkout_url: str | Unset = UNSET
    complete_token: str | Unset = UNSET
    """Completes the upgrade after Checkout."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan_id = str(self.plan_id)

        effective_from: str | Unset = UNSET
        if not isinstance(self.effective_from, Unset):
            effective_from = self.effective_from.isoformat()

        checkout_url = self.checkout_url

        complete_token = self.complete_token

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan_id": plan_id,
            }
        )
        if effective_from is not UNSET:
            field_dict["effective_from"] = effective_from
        if checkout_url is not UNSET:
            field_dict["checkout_url"] = checkout_url
        if complete_token is not UNSET:
            field_dict["complete_token"] = complete_token

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        plan_id = UUID(d.pop("plan_id"))

        _effective_from = d.pop("effective_from", UNSET)
        effective_from: datetime.datetime | Unset
        if isinstance(_effective_from, Unset):
            effective_from = UNSET
        else:
            effective_from = datetime.datetime.fromisoformat(_effective_from)

        checkout_url = d.pop("checkout_url", UNSET)

        complete_token = d.pop("complete_token", UNSET)

        consumer_upgrade_result = cls(
            plan_id=plan_id,
            effective_from=effective_from,
            checkout_url=checkout_url,
            complete_token=complete_token,
        )

        consumer_upgrade_result.additional_properties = d
        return consumer_upgrade_result

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
