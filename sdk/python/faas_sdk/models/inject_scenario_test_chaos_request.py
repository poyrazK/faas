from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.scenario_test_chaos_rule import ScenarioTestChaosRule


T = TypeVar("T", bound="InjectScenarioTestChaosRequest")


@_attrs_define
class InjectScenarioTestChaosRequest:
    """Bounded fault plan applied only to service calls within one scenario test namespace."""

    duration_ms: int
    rules: list[ScenarioTestChaosRule]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        duration_ms = self.duration_ms

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "duration_ms": duration_ms,
                "rules": rules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_test_chaos_rule import ScenarioTestChaosRule

        d = dict(src_dict)
        duration_ms = d.pop("duration_ms")

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = ScenarioTestChaosRule.from_dict(rules_item_data)

            rules.append(rules_item)

        inject_scenario_test_chaos_request = cls(
            duration_ms=duration_ms,
            rules=rules,
        )

        inject_scenario_test_chaos_request.additional_properties = d
        return inject_scenario_test_chaos_request

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
