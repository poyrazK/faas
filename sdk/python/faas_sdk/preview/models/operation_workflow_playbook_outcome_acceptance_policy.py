from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowPlaybookOutcomeAcceptancePolicy")


@_attrs_define
class OperationWorkflowPlaybookOutcomeAcceptancePolicy:
    """OperationWorkflowPlaybookOutcomeAcceptancePolicy. Explicit business acceptance rules pinned with the playbook.
    Requires an expected outcome; an empty list permits no alternatives. Omission preserves legacy reviewer acceptance.

    """

    alternative_outcome_codes: list[str]
    """OperationWorkflowPlaybookOutcomeAcceptancePolicy.alternative_outcome_codes. Distinct application-defined
    terminal outcome slugs, excluding the primary expected outcome. Sorted canonically; never overrides application
    reports or the primary deadline target."""

    def to_dict(self) -> dict[str, Any]:
        alternative_outcome_codes = self.alternative_outcome_codes

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "alternative_outcome_codes": alternative_outcome_codes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        alternative_outcome_codes = cast(list[str], d.pop("alternative_outcome_codes"))

        operation_workflow_playbook_outcome_acceptance_policy = cls(
            alternative_outcome_codes=alternative_outcome_codes,
        )

        return operation_workflow_playbook_outcome_acceptance_policy
