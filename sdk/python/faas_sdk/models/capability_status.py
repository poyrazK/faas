from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.capability_status_maturity import CapabilityStatusMaturity, check_capability_status_maturity
from ..models.capability_status_plans_item import CapabilityStatusPlansItem, check_capability_status_plans_item

T = TypeVar("T", bound="CapabilityStatus")


@_attrs_define
class CapabilityStatus:
    """Customer-visible capability with the account plan gate resolved."""

    key: str
    name: str
    category: str
    description: str
    maturity: CapabilityStatusMaturity
    plans: list[CapabilityStatusPlansItem]
    docs_url: str
    acceptance: str
    """Stable acceptance-test handle for this capability."""
    enabled: bool
    """Whether the calling account may use the capability under its current plan."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        name = self.name

        category = self.category

        description = self.description

        maturity: str = self.maturity

        plans = []
        for plans_item_data in self.plans:
            plans_item: str = plans_item_data
            plans.append(plans_item)

        docs_url = self.docs_url

        acceptance = self.acceptance

        enabled = self.enabled

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "name": name,
                "category": category,
                "description": description,
                "maturity": maturity,
                "plans": plans,
                "docs_url": docs_url,
                "acceptance": acceptance,
                "enabled": enabled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        name = d.pop("name")

        category = d.pop("category")

        description = d.pop("description")

        maturity = check_capability_status_maturity(d.pop("maturity"))

        plans = []
        _plans = d.pop("plans")
        for plans_item_data in _plans:
            plans_item = check_capability_status_plans_item(plans_item_data)

            plans.append(plans_item)

        docs_url = d.pop("docs_url")

        acceptance = d.pop("acceptance")

        enabled = d.pop("enabled")

        capability_status = cls(
            key=key,
            name=name,
            category=category,
            description=description,
            maturity=maturity,
            plans=plans,
            docs_url=docs_url,
            acceptance=acceptance,
            enabled=enabled,
        )

        capability_status.additional_properties = d
        return capability_status

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
