from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.consumer_credit_pack import ConsumerCreditPack


T = TypeVar("T", bound="ConsumerCreditPacksResponse")


@_attrs_define
class ConsumerCreditPacksResponse:
    """The credit packs this consumer can buy."""

    packs: list[ConsumerCreditPack]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        packs = []
        for packs_item_data in self.packs:
            packs_item = packs_item_data.to_dict()
            packs.append(packs_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "packs": packs,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.consumer_credit_pack import ConsumerCreditPack

        d = dict(src_dict)
        packs = []
        _packs = d.pop("packs")
        for packs_item_data in _packs:
            packs_item = ConsumerCreditPack.from_dict(packs_item_data)

            packs.append(packs_item)

        consumer_credit_packs_response = cls(
            packs=packs,
        )

        consumer_credit_packs_response.additional_properties = d
        return consumer_credit_packs_response

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
