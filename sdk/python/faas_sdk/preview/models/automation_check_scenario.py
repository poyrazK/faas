from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="AutomationCheckScenario")


@_attrs_define
class AutomationCheckScenario:
    """A named scenario reported as passing, definition valid, and complete by the publishing client."""

    name: str
    passed: bool
    definition_valid: bool
    complete: bool

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        passed = self.passed

        definition_valid = self.definition_valid

        complete = self.complete

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "passed": passed,
                "definition_valid": definition_valid,
                "complete": complete,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        passed = d.pop("passed")

        definition_valid = d.pop("definition_valid")

        complete = d.pop("complete")

        automation_check_scenario = cls(
            name=name,
            passed=passed,
            definition_valid=definition_valid,
            complete=complete,
        )

        return automation_check_scenario
