from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CreateConsumerCreditPurchaseRequest")


@_attrs_define
class CreateConsumerCreditPurchaseRequest:
    """A pack selection and the UUID used to recover the purchase on retry."""

    purchase_id: UUID
    """A unique client-generated identifier. Reuse it to retry this purchase."""
    pack_id: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        purchase_id = str(self.purchase_id)

        pack_id = self.pack_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "purchase_id": purchase_id,
                "pack_id": pack_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        purchase_id = UUID(d.pop("purchase_id"))

        pack_id = d.pop("pack_id")

        create_consumer_credit_purchase_request = cls(
            purchase_id=purchase_id,
            pack_id=pack_id,
        )

        create_consumer_credit_purchase_request.additional_properties = d
        return create_consumer_credit_purchase_request

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
