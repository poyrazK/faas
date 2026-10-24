from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleGeoAction")


@_attrs_define
class EdgeRuleGeoAction:
    """ISO 3166-1 alpha-2 country allow/deny evaluator (ADR-091
    D21 / §4.1.2.8b). Mirrors `EdgeRuleIPAction` exactly: `allow`
    and `deny` are parallel ISO 3166-1 alpha-2 country-code
    lists; the matcher walks deny AFTER allow so a single-country
    deny wins even when the allow list is broad.

    The match port is `gatewayd-internal` which consults a
    DB-IP Lite `.mmdb` file at request time to translate the
    trusted XFF client IP into a country code. Plan-tier quota
    is enforced at apid-create time via `Limits.EdgeRulesGeoPerApp`
    (Free=1, Hobby=5, Pro=25, Scale=100) inside the same apps-row
    FOR UPDATE lock as the general edge-rule cap. Geo is NOT in
    IsPaidOnly — Free customers get one rule before they upgrade.

    Failure posture is fail-open: missing `.mmdb`, IP not in
    any country, RFC1918/bogon, or corrupt file → the rule does
    not fire → the request flows through. The
    `gateway_edge_rule_match_total{kind="geo",result="failed"}`
    counter increments and an `edge_rule.geo_failed` audit event
    emits.

    """

    allow: list[str] | Unset = UNSET
    """ISO 3166-1 alpha-2 country allowlist. Empty + deny non-empty = deny-only. Empty + deny empty = no-op
    (create-time 422)."""
    deny: list[str] | Unset = UNSET
    """ISO 3166-1 alpha-2 country denylist. Evaluated AFTER allow; single-country deny wins."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        allow: list[str] | Unset = UNSET
        if not isinstance(self.allow, Unset):
            allow = self.allow

        deny: list[str] | Unset = UNSET
        if not isinstance(self.deny, Unset):
            deny = self.deny

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if allow is not UNSET:
            field_dict["allow"] = allow
        if deny is not UNSET:
            field_dict["deny"] = deny

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        allow = cast(list[str], d.pop("allow", UNSET))

        deny = cast(list[str], d.pop("deny", UNSET))

        edge_rule_geo_action = cls(
            allow=allow,
            deny=deny,
        )

        edge_rule_geo_action.additional_properties = d
        return edge_rule_geo_action

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
