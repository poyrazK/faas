from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_advice_policy_rule_kind import RouteAdvicePolicyRuleKind, check_route_advice_policy_rule_kind
from ..models.route_advice_policy_rule_mode import RouteAdvicePolicyRuleMode, check_route_advice_policy_rule_mode
from ..models.route_advice_policy_rule_source import RouteAdvicePolicyRuleSource, check_route_advice_policy_rule_source
from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteAdvicePolicyRule")


@_attrs_define
class RouteAdvicePolicyRule:
    """Credential-free selectors and revision metadata for one selected-app desired rule. Cache methods reflect the
    intersection of selector and cache action allowlist.

    """

    id: str
    """Identifier of this consented app's policy rule."""
    kind: RouteAdvicePolicyRuleKind
    """Route-advice policy kind being inspected."""
    source: RouteAdvicePolicyRuleSource
    """Whether the rule comes from the selected hostname, app-wide selector or independent environment cache
    policy."""
    path: str
    """Observed rule path selector, evaluated with gateway matching semantics."""
    methods: list[str]
    """Effective policy method selectors; empty means unrestricted for non-cache rules."""
    priority: int
    """Desired rule priority. Equal-priority candidates remain uncertain."""
    conditional: bool
    """The rule requires headers or structured request conditions that telemetry does not supply."""
    mode: RouteAdvicePolicyRuleMode
    """Configured enforcement mode; log-only matches do not prove an active policy effect."""
    updated_at: datetime.datetime
    """Rule update metadata used by the scoped observation fingerprint."""
    expires_at: datetime.datetime | Unset = UNSET
    """Policy expiry boundary when one is configured."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        kind: str = self.kind

        source: str = self.source

        path = self.path

        methods = self.methods

        priority = self.priority

        conditional = self.conditional

        mode: str = self.mode

        updated_at = self.updated_at.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "kind": kind,
                "source": source,
                "path": path,
                "methods": methods,
                "priority": priority,
                "conditional": conditional,
                "mode": mode,
                "updated_at": updated_at,
            }
        )
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        kind = check_route_advice_policy_rule_kind(d.pop("kind"))

        source = check_route_advice_policy_rule_source(d.pop("source"))

        path = d.pop("path")

        methods = cast(list[str], d.pop("methods"))

        priority = d.pop("priority")

        conditional = d.pop("conditional")

        mode = check_route_advice_policy_rule_mode(d.pop("mode"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        route_advice_policy_rule = cls(
            id=id,
            kind=kind,
            source=source,
            path=path,
            methods=methods,
            priority=priority,
            conditional=conditional,
            mode=mode,
            updated_at=updated_at,
            expires_at=expires_at,
        )

        route_advice_policy_rule.additional_properties = d
        return route_advice_policy_rule

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
