from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.failure_rules_uncertain_outcome import FailureRulesUncertainOutcome, check_failure_rules_uncertain_outcome
from ..models.failure_rules_unmatched_failure import FailureRulesUnmatchedFailure, check_failure_rules_unmatched_failure
from ..models.failure_rules_version import FailureRulesVersion, check_failure_rules_version

if TYPE_CHECKING:
    from ..models.failure_rule import FailureRule


T = TypeVar("T", bound="FailureRules")


@_attrs_define
class FailureRules:
    """Versioned explicit classification policy for failed Job partitions and command-Cron executions. HTTP Crons do not
    accept failure rules.

    """

    version: FailureRulesVersion
    rules: list[FailureRule]
    unmatched_failure: FailureRulesUnmatchedFailure
    uncertain_outcome: FailureRulesUncertainOutcome
    """Choose whether a missing completion receipt stays held for reconciliation or is retried with possible
    duplicate execution."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        unmatched_failure: str = self.unmatched_failure

        uncertain_outcome: str = self.uncertain_outcome

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "rules": rules,
                "unmatched_failure": unmatched_failure,
                "uncertain_outcome": uncertain_outcome,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.failure_rule import FailureRule

        d = dict(src_dict)
        version = check_failure_rules_version(d.pop("version"))

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = FailureRule.from_dict(rules_item_data)

            rules.append(rules_item)

        unmatched_failure = check_failure_rules_unmatched_failure(d.pop("unmatched_failure"))

        uncertain_outcome = check_failure_rules_uncertain_outcome(d.pop("uncertain_outcome"))

        failure_rules = cls(
            version=version,
            rules=rules,
            unmatched_failure=unmatched_failure,
            uncertain_outcome=uncertain_outcome,
        )

        failure_rules.additional_properties = d
        return failure_rules

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
