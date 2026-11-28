from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.failure_rule_action import FailureRuleAction, check_failure_rule_action
from ..types import UNSET, Unset

T = TypeVar("T", bound="FailureRule")


@_attrs_define
class FailureRule:
    """A guest exit-code or structured application-outcome matcher and the action for that confirmed result. HTTP Crons
    support outcome_codes; exit_codes apply to Jobs and command Crons.

    """

    action: FailureRuleAction
    exit_codes: list[int] | Unset = UNSET
    outcome_codes: list[str] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        exit_codes: list[int] | Unset = UNSET
        if not isinstance(self.exit_codes, Unset):
            exit_codes = self.exit_codes

        outcome_codes: list[str] | Unset = UNSET
        if not isinstance(self.outcome_codes, Unset):
            outcome_codes = self.outcome_codes

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "action": action,
            }
        )
        if exit_codes is not UNSET:
            field_dict["exit_codes"] = exit_codes
        if outcome_codes is not UNSET:
            field_dict["outcome_codes"] = outcome_codes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_failure_rule_action(d.pop("action"))

        exit_codes = cast(list[int], d.pop("exit_codes", UNSET))

        outcome_codes = cast(list[str], d.pop("outcome_codes", UNSET))

        failure_rule = cls(
            action=action,
            exit_codes=exit_codes,
            outcome_codes=outcome_codes,
        )

        return failure_rule
