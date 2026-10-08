from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_response import EdgeRuleResponse


T = TypeVar("T", bound="EdgeRuleSetVersionResponse")


@_attrs_define
class EdgeRuleSetVersionResponse:
    """One recorded state of an app's whole edge-rule set (ADR-732)."""

    version: int
    rule_count: int
    rules_sha256: str
    """Digest of the recorded rule set."""
    created_at: datetime.datetime
    current: bool
    """True for the app's latest version."""
    rules: list[EdgeRuleResponse] | Unset = UNSET
    """The recorded rules; present only when a single version is fetched."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        rule_count = self.rule_count

        rules_sha256 = self.rules_sha256

        created_at = self.created_at.isoformat()

        current = self.current

        rules: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.rules, Unset):
            rules = []
            for rules_item_data in self.rules:
                rules_item = rules_item_data.to_dict()
                rules.append(rules_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "rule_count": rule_count,
                "rules_sha256": rules_sha256,
                "created_at": created_at,
                "current": current,
            }
        )
        if rules is not UNSET:
            field_dict["rules"] = rules

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_response import EdgeRuleResponse

        d = dict(src_dict)
        version = d.pop("version")

        rule_count = d.pop("rule_count")

        rules_sha256 = d.pop("rules_sha256")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        current = d.pop("current")

        _rules = d.pop("rules", UNSET)
        rules: list[EdgeRuleResponse] | Unset = UNSET
        if _rules is not UNSET:
            rules = []
            for rules_item_data in _rules:
                rules_item = EdgeRuleResponse.from_dict(rules_item_data)

                rules.append(rules_item)

        edge_rule_set_version_response = cls(
            version=version,
            rule_count=rule_count,
            rules_sha256=rules_sha256,
            created_at=created_at,
            current=current,
            rules=rules,
        )

        edge_rule_set_version_response.additional_properties = d
        return edge_rule_set_version_response

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
