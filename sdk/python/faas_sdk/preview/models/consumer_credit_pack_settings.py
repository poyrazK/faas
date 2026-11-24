from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.consumer_credit_pack import ConsumerCreditPack


T = TypeVar("T", bound="ConsumerCreditPackSettings")


@_attrs_define
class ConsumerCreditPackSettings:
    """The packs offered by an app and the billing page where buyers return."""

    packs: list[ConsumerCreditPack]
    return_url: str | Unset = UNSET
    """The HTTPS billing page to return to, required when packs are offered."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        packs = []
        for packs_item_data in self.packs:
            packs_item = packs_item_data.to_dict()
            packs.append(packs_item)

        return_url = self.return_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "packs": packs,
            }
        )
        if return_url is not UNSET:
            field_dict["return_url"] = return_url

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

        return_url = d.pop("return_url", UNSET)

        consumer_credit_pack_settings = cls(
            packs=packs,
            return_url=return_url,
        )

        consumer_credit_pack_settings.additional_properties = d
        return consumer_credit_pack_settings

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
