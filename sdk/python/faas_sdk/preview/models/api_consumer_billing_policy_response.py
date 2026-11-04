from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="APIConsumerBillingPolicyResponse")


@_attrs_define
class APIConsumerBillingPolicyResponse:
    """An app's stored overdue-invoice rule."""

    app_id: UUID
    block_overdue_after_days: int
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        block_overdue_after_days = self.block_overdue_after_days

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "block_overdue_after_days": block_overdue_after_days,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        block_overdue_after_days = d.pop("block_overdue_after_days")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        api_consumer_billing_policy_response = cls(
            app_id=app_id,
            block_overdue_after_days=block_overdue_after_days,
            updated_at=updated_at,
        )

        api_consumer_billing_policy_response.additional_properties = d
        return api_consumer_billing_policy_response

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
