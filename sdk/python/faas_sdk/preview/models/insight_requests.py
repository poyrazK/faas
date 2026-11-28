from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.insight_request_variant import InsightRequestVariant


T = TypeVar("T", bound="InsightRequests")


@_attrs_define
class InsightRequests:
    """Request telemetry by flag value."""

    flag: str
    from_: datetime.datetime
    until: datetime.datetime
    variants: list[InsightRequestVariant]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        flag = self.flag

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        variants = []
        for variants_item_data in self.variants:
            variants_item = variants_item_data.to_dict()
            variants.append(variants_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "flag": flag,
                "from": from_,
                "until": until,
                "variants": variants,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_request_variant import InsightRequestVariant

        d = dict(src_dict)
        flag = d.pop("flag")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        variants = []
        _variants = d.pop("variants")
        for variants_item_data in _variants:
            variants_item = InsightRequestVariant.from_dict(variants_item_data)

            variants.append(variants_item)

        insight_requests = cls(
            flag=flag,
            from_=from_,
            until=until,
            variants=variants,
        )

        insight_requests.additional_properties = d
        return insight_requests

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
