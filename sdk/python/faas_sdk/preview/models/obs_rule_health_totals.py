from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRuleHealthTotals")


@_attrs_define
class ObsRuleHealthTotals:
    """Complete bounded source counts. Rule health categories partition rules; invalid groups include empty groups with
    missing or duplicate identity.

    """

    groups: int
    rules: int
    healthy: int
    failed: int
    stale: int
    invalid_clock: int
    unavailable: int
    invalid: int
    invalid_groups: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        groups = self.groups

        rules = self.rules

        healthy = self.healthy

        failed = self.failed

        stale = self.stale

        invalid_clock = self.invalid_clock

        unavailable = self.unavailable

        invalid = self.invalid

        invalid_groups = self.invalid_groups

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "groups": groups,
                "rules": rules,
                "healthy": healthy,
                "failed": failed,
                "stale": stale,
                "invalid_clock": invalid_clock,
                "unavailable": unavailable,
                "invalid": invalid,
                "invalid_groups": invalid_groups,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        groups = d.pop("groups")

        rules = d.pop("rules")

        healthy = d.pop("healthy")

        failed = d.pop("failed")

        stale = d.pop("stale")

        invalid_clock = d.pop("invalid_clock")

        unavailable = d.pop("unavailable")

        invalid = d.pop("invalid")

        invalid_groups = d.pop("invalid_groups")

        obs_rule_health_totals = cls(
            groups=groups,
            rules=rules,
            healthy=healthy,
            failed=failed,
            stale=stale,
            invalid_clock=invalid_clock,
            unavailable=unavailable,
            invalid=invalid,
            invalid_groups=invalid_groups,
        )

        obs_rule_health_totals.additional_properties = d
        return obs_rule_health_totals

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
