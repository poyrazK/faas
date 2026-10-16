from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_advice_policy_context_status import (
    RouteAdvicePolicyContextStatus,
    check_route_advice_policy_context_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_advice_policy_rule import RouteAdvicePolicyRule


T = TypeVar("T", bound="RouteAdvicePolicyContext")


@_attrs_define
class RouteAdvicePolicyContext:
    """Bounded observation of selected-app desired cache, async and throttle rules matching the selected host, plus app-
    wide rules. Omits action and condition values; not proof of gateway convergence or authority to write.

    """

    status: RouteAdvicePolicyContextStatus
    """Policy snapshot availability; changed means re-reading found a different observation."""
    observed_at: datetime.datetime
    """Time the desired-policy observation was collected."""
    rules: list[RouteAdvicePolicyRule]
    gaps: list[str]
    """Stable coverage reasons. Runtime request attributes, disabled drafts, other gateway gates and actual
    convergence are not established by this read."""
    revision: str | Unset = UNSET
    """Opaque fingerprint of scoped rule identities and update metadata; present only for observed snapshots, never
    an apply guard or account activity counter."""
    host: str | Unset = UNSET
    """Exact selected platform or immutable named-environment hostname."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        observed_at = self.observed_at.isoformat()

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        gaps = self.gaps

        revision = self.revision

        host = self.host

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "observed_at": observed_at,
                "rules": rules,
                "gaps": gaps,
            }
        )
        if revision is not UNSET:
            field_dict["revision"] = revision
        if host is not UNSET:
            field_dict["host"] = host

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_advice_policy_rule import RouteAdvicePolicyRule

        d = dict(src_dict)
        status = check_route_advice_policy_context_status(d.pop("status"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = RouteAdvicePolicyRule.from_dict(rules_item_data)

            rules.append(rules_item)

        gaps = cast(list[str], d.pop("gaps"))

        revision = d.pop("revision", UNSET)

        host = d.pop("host", UNSET)

        route_advice_policy_context = cls(
            status=status,
            observed_at=observed_at,
            rules=rules,
            gaps=gaps,
            revision=revision,
            host=host,
        )

        route_advice_policy_context.additional_properties = d
        return route_advice_policy_context

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
