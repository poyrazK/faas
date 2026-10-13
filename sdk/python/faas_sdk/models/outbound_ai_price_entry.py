from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundAIPriceEntry")


@_attrs_define
class OutboundAIPriceEntry:
    """Price for model names starting with model_prefix ("*" matches any model; the longest prefix wins), in millicents per
    million tokens. Absent cached or cache-write prices use the input price.

    """

    model_prefix: str
    input_millicents_per_mtok: int
    output_millicents_per_mtok: int
    cached_input_millicents_per_mtok: int | Unset = UNSET
    cache_write_millicents_per_mtok: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        model_prefix = self.model_prefix

        input_millicents_per_mtok = self.input_millicents_per_mtok

        output_millicents_per_mtok = self.output_millicents_per_mtok

        cached_input_millicents_per_mtok = self.cached_input_millicents_per_mtok

        cache_write_millicents_per_mtok = self.cache_write_millicents_per_mtok

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "model_prefix": model_prefix,
                "input_millicents_per_mtok": input_millicents_per_mtok,
                "output_millicents_per_mtok": output_millicents_per_mtok,
            }
        )
        if cached_input_millicents_per_mtok is not UNSET:
            field_dict["cached_input_millicents_per_mtok"] = cached_input_millicents_per_mtok
        if cache_write_millicents_per_mtok is not UNSET:
            field_dict["cache_write_millicents_per_mtok"] = cache_write_millicents_per_mtok

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        model_prefix = d.pop("model_prefix")

        input_millicents_per_mtok = d.pop("input_millicents_per_mtok")

        output_millicents_per_mtok = d.pop("output_millicents_per_mtok")

        cached_input_millicents_per_mtok = d.pop("cached_input_millicents_per_mtok", UNSET)

        cache_write_millicents_per_mtok = d.pop("cache_write_millicents_per_mtok", UNSET)

        outbound_ai_price_entry = cls(
            model_prefix=model_prefix,
            input_millicents_per_mtok=input_millicents_per_mtok,
            output_millicents_per_mtok=output_millicents_per_mtok,
            cached_input_millicents_per_mtok=cached_input_millicents_per_mtok,
            cache_write_millicents_per_mtok=cache_write_millicents_per_mtok,
        )

        outbound_ai_price_entry.additional_properties = d
        return outbound_ai_price_entry

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
