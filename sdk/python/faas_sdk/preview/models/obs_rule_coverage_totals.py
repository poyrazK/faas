from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuleCoverageTotals")


@_attrs_define
class ObsRuleCoverageTotals:
    """Full counts before preview truncation. Expected categories partition expected_rules; additional valid identities are
    allowed by the minimum set.

    """

    expected_rules: int
    observed_rules: int
    additional_rules: int
    invalid_observed_rules: int
    invalid_observed_groups: int
    matching: int
    missing: int
    changed: int
    ambiguous: int
    unknown: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_rules = self.expected_rules

        observed_rules = self.observed_rules

        additional_rules = self.additional_rules

        invalid_observed_rules = self.invalid_observed_rules

        invalid_observed_groups = self.invalid_observed_groups

        matching = self.matching

        missing = self.missing

        changed = self.changed

        ambiguous = self.ambiguous

        unknown = self.unknown

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_rules": expected_rules,
                "observed_rules": observed_rules,
                "additional_rules": additional_rules,
                "invalid_observed_rules": invalid_observed_rules,
                "invalid_observed_groups": invalid_observed_groups,
                "matching": matching,
                "missing": missing,
                "changed": changed,
                "ambiguous": ambiguous,
                "unknown": unknown,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_rules = d.pop("expected_rules")

        observed_rules = d.pop("observed_rules")

        additional_rules = d.pop("additional_rules")

        invalid_observed_rules = d.pop("invalid_observed_rules")

        invalid_observed_groups = d.pop("invalid_observed_groups")

        matching = d.pop("matching")

        missing = d.pop("missing")

        changed = d.pop("changed")

        ambiguous = d.pop("ambiguous")

        unknown = d.pop("unknown")

        obs_rule_coverage_totals = cls(
            expected_rules=expected_rules,
            observed_rules=observed_rules,
            additional_rules=additional_rules,
            invalid_observed_rules=invalid_observed_rules,
            invalid_observed_groups=invalid_observed_groups,
            matching=matching,
            missing=missing,
            changed=changed,
            ambiguous=ambiguous,
            unknown=unknown,
        )

        obs_rule_coverage_totals.additional_properties = d
        return obs_rule_coverage_totals

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
