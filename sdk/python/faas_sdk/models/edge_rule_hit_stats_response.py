from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleHitStatsResponse")


@_attrs_define
class EdgeRuleHitStatsResponse:
    """One rule's match counts over the window (ADR-960)."""

    rule_id: str
    matched: int
    """Requests an enforced rule matched."""
    logged: int
    """Requests a log-mode rule matched."""
    series: list[int] | Unset = UNSET
    """With series=true, matched plus logged per hour; the first value is the hour starting at since, the last the
    current hour (ADR-20261020105001)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rule_id = self.rule_id

        matched = self.matched

        logged = self.logged

        series: list[int] | Unset = UNSET
        if not isinstance(self.series, Unset):
            series = self.series

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rule_id": rule_id,
                "matched": matched,
                "logged": logged,
            }
        )
        if series is not UNSET:
            field_dict["series"] = series

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rule_id = d.pop("rule_id")

        matched = d.pop("matched")

        logged = d.pop("logged")

        series = cast(list[int], d.pop("series", UNSET))

        edge_rule_hit_stats_response = cls(
            rule_id=rule_id,
            matched=matched,
            logged=logged,
            series=series,
        )

        edge_rule_hit_stats_response.additional_properties = d
        return edge_rule_hit_stats_response

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
