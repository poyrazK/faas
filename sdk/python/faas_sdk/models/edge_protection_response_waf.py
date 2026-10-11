from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.edge_protection_response_waf_categories_item import EdgeProtectionResponseWafCategoriesItem
    from ..models.edge_protection_response_waf_top_rules_item import EdgeProtectionResponseWafTopRulesItem


T = TypeVar("T", bound="EdgeProtectionResponseWaf")


@_attrs_define
class EdgeProtectionResponseWaf:
    """kind=waf inspections (ADR-831). Sampled detections are never blocked; block rules' 403s also appear in rejections
    under gate waf.

    """

    inspected: int
    """Requests scored by the OWASP CRS."""
    detected: int
    """Inspected requests at or above the rule's anomaly threshold."""
    not_inspected: int
    """Matched requests skipped to protect the node (budget, full queue, or error)."""
    warned: int
    """Requests a warn rule detected in-path and tagged with X-WAF-Warning."""
    blocked: int
    """Requests a block rule detected in-path and answered with 403."""
    inline_skipped: int
    """Requests that passed a warn or block rule unchecked because the in-path budget was exhausted."""
    categories: list[EdgeProtectionResponseWafCategoriesItem]
    """Detections by CRS attack category, largest first; one detection may count several."""
    top_rules: list[EdgeProtectionResponseWafTopRulesItem]
    """CRS rule IDs that scored most, largest first, for tuning exclude_rule_ids."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        inspected = self.inspected

        detected = self.detected

        not_inspected = self.not_inspected

        warned = self.warned

        blocked = self.blocked

        inline_skipped = self.inline_skipped

        categories = []
        for categories_item_data in self.categories:
            categories_item = categories_item_data.to_dict()
            categories.append(categories_item)

        top_rules = []
        for top_rules_item_data in self.top_rules:
            top_rules_item = top_rules_item_data.to_dict()
            top_rules.append(top_rules_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "inspected": inspected,
                "detected": detected,
                "not_inspected": not_inspected,
                "warned": warned,
                "blocked": blocked,
                "inline_skipped": inline_skipped,
                "categories": categories,
                "top_rules": top_rules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_protection_response_waf_categories_item import EdgeProtectionResponseWafCategoriesItem
        from ..models.edge_protection_response_waf_top_rules_item import EdgeProtectionResponseWafTopRulesItem

        d = dict(src_dict)
        inspected = d.pop("inspected")

        detected = d.pop("detected")

        not_inspected = d.pop("not_inspected")

        warned = d.pop("warned")

        blocked = d.pop("blocked")

        inline_skipped = d.pop("inline_skipped")

        categories = []
        _categories = d.pop("categories")
        for categories_item_data in _categories:
            categories_item = EdgeProtectionResponseWafCategoriesItem.from_dict(categories_item_data)

            categories.append(categories_item)

        top_rules = []
        _top_rules = d.pop("top_rules")
        for top_rules_item_data in _top_rules:
            top_rules_item = EdgeProtectionResponseWafTopRulesItem.from_dict(top_rules_item_data)

            top_rules.append(top_rules_item)

        edge_protection_response_waf = cls(
            inspected=inspected,
            detected=detected,
            not_inspected=not_inspected,
            warned=warned,
            blocked=blocked,
            inline_skipped=inline_skipped,
            categories=categories,
            top_rules=top_rules,
        )

        edge_protection_response_waf.additional_properties = d
        return edge_protection_response_waf

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
