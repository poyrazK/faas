from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_protection_ai_crawler_purpose import (
    EdgeProtectionAICrawlerPurpose,
    check_edge_protection_ai_crawler_purpose,
)

T = TypeVar("T", bound="EdgeProtectionAICrawler")


@_attrs_define
class EdgeProtectionAICrawler:
    """One AI crawler's requests over the summary's range."""

    name: str
    """Crawler name as the ai_crawler match field uses it, e.g. gptbot."""
    operator: str
    """Company that runs the crawler."""
    purpose: EdgeProtectionAICrawlerPurpose
    """What its fetches are for: training models, AI search, or an assistant answering a user."""
    count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        operator = self.operator

        purpose: str = self.purpose

        count = self.count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "operator": operator,
                "purpose": purpose,
                "count": count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        operator = d.pop("operator")

        purpose = check_edge_protection_ai_crawler_purpose(d.pop("purpose"))

        count = d.pop("count")

        edge_protection_ai_crawler = cls(
            name=name,
            operator=operator,
            purpose=purpose,
            count=count,
        )

        edge_protection_ai_crawler.additional_properties = d
        return edge_protection_ai_crawler

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
