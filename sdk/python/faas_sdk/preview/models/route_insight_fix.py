from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_insight_fix_action import RouteInsightFixAction, check_route_insight_fix_action
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteInsightFix")


@_attrs_define
class RouteInsightFix:
    """The edge-rule change applying an active insight would make now, planned from its evidence and the app's current
    rules. Absent when the platform has no fix; never in webhook payloads.

    """

    id: str
    """Covers every value the fix would write; send it back to apply exactly this fix."""
    action: RouteInsightFixAction
    summary: str | Unset = UNSET
    max_age_seconds: int | Unset = UNSET
    rule_id: str | Unset = UNSET
    """The cache rule the fix changes or overrides."""
    purge_cache: bool | Unset = UNSET
    """The fix also purges the route's cached responses."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        action: str = self.action

        summary = self.summary

        max_age_seconds = self.max_age_seconds

        rule_id = self.rule_id

        purge_cache = self.purge_cache

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "action": action,
            }
        )
        if summary is not UNSET:
            field_dict["summary"] = summary
        if max_age_seconds is not UNSET:
            field_dict["max_age_seconds"] = max_age_seconds
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if purge_cache is not UNSET:
            field_dict["purge_cache"] = purge_cache

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        action = check_route_insight_fix_action(d.pop("action"))

        summary = d.pop("summary", UNSET)

        max_age_seconds = d.pop("max_age_seconds", UNSET)

        rule_id = d.pop("rule_id", UNSET)

        purge_cache = d.pop("purge_cache", UNSET)

        route_insight_fix = cls(
            id=id,
            action=action,
            summary=summary,
            max_age_seconds=max_age_seconds,
            rule_id=rule_id,
            purge_cache=purge_cache,
        )

        route_insight_fix.additional_properties = d
        return route_insight_fix

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
