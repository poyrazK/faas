from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.operator_platform_flag_decision import OperatorPlatformFlagDecision


T = TypeVar("T", bound="OperatorAccountPlatformFlags")


@_attrs_define
class OperatorAccountPlatformFlags:
    """Every platform flag decided for one account."""

    account_id: UUID
    plan: str
    items: list[OperatorPlatformFlagDecision]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        plan = self.plan

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "plan": plan,
                "items": items,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operator_platform_flag_decision import OperatorPlatformFlagDecision

        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        plan = d.pop("plan")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperatorPlatformFlagDecision.from_dict(items_item_data)

            items.append(items_item)

        operator_account_platform_flags = cls(
            account_id=account_id,
            plan=plan,
            items=items,
        )

        operator_account_platform_flags.additional_properties = d
        return operator_account_platform_flags

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
