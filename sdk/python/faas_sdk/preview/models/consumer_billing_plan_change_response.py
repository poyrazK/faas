from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ConsumerBillingPlanChangeResponse")


@_attrs_define
class ConsumerBillingPlanChangeResponse:
    """A scheduled plan change."""

    plan_id: UUID
    effective_from: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan_id = str(self.plan_id)

        effective_from = self.effective_from.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan_id": plan_id,
                "effective_from": effective_from,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        plan_id = UUID(d.pop("plan_id"))

        effective_from = datetime.datetime.fromisoformat(d.pop("effective_from"))

        consumer_billing_plan_change_response = cls(
            plan_id=plan_id,
            effective_from=effective_from,
        )

        consumer_billing_plan_change_response.additional_properties = d
        return consumer_billing_plan_change_response

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
