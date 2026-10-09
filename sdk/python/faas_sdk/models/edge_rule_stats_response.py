from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_stats_response_window import EdgeRuleStatsResponseWindow, check_edge_rule_stats_response_window

if TYPE_CHECKING:
    from ..models.edge_rule_hit_stats_response import EdgeRuleHitStatsResponse


T = TypeVar("T", bound="EdgeRuleStatsResponse")


@_attrs_define
class EdgeRuleStatsResponse:
    """Per-rule match counts over a window (ADR-904)."""

    window: EdgeRuleStatsResponseWindow
    since: datetime.datetime
    rules: list[EdgeRuleHitStatsResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        window: str = self.window

        since = self.since.isoformat()

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "window": window,
                "since": since,
                "rules": rules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_hit_stats_response import EdgeRuleHitStatsResponse

        d = dict(src_dict)
        window = check_edge_rule_stats_response_window(d.pop("window"))

        since = datetime.datetime.fromisoformat(d.pop("since"))

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = EdgeRuleHitStatsResponse.from_dict(rules_item_data)

            rules.append(rules_item)

        edge_rule_stats_response = cls(
            window=window,
            since=since,
            rules=rules,
        )

        edge_rule_stats_response.additional_properties = d
        return edge_rule_stats_response

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
