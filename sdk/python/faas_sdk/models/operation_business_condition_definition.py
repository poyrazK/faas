from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationBusinessConditionDefinition")


@_attrs_define
class OperationBusinessConditionDefinition:
    """Public condition vocabulary declared on a pinned workflow milestone step. A definition does not evaluate a
    predicate.

    """

    id: str
    description: str
    """Public UTF-8 explanation without control characters; byte bound."""
    expected_states: list[str] | Unset = UNSET
    """Optional states where this workflow step is expected to report the condition. Names must belong to its
    declared workflow states. Omission declares no expectation; this never gates transitions or evaluates business
    truth."""
    refresh_owner: str | Unset = UNSET
    """Optional public team or actor responsible for refreshing this condition report. UTF-8 byte bound; no
    controls or surrounding whitespace. Omission is unassigned."""
    refresh_instructions: str | Unset = UNSET
    """Optional public application-declared refresh instructions. UTF-8 byte bound; no controls or surrounding
    whitespace. Displayed as text and never executed."""
    evidence_max_age_seconds: int | Unset = UNSET
    """Optional pinned maximum condition report age in seconds. Zero or omission disables the budget; this measures
    reporting freshness without changing the application result."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        description = self.description

        expected_states: list[str] | Unset = UNSET
        if not isinstance(self.expected_states, Unset):
            expected_states = self.expected_states

        refresh_owner = self.refresh_owner

        refresh_instructions = self.refresh_instructions

        evidence_max_age_seconds = self.evidence_max_age_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "description": description,
            }
        )
        if expected_states is not UNSET:
            field_dict["expected_states"] = expected_states
        if refresh_owner is not UNSET:
            field_dict["refresh_owner"] = refresh_owner
        if refresh_instructions is not UNSET:
            field_dict["refresh_instructions"] = refresh_instructions
        if evidence_max_age_seconds is not UNSET:
            field_dict["evidence_max_age_seconds"] = evidence_max_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        description = d.pop("description")

        expected_states = cast(list[str], d.pop("expected_states", UNSET))

        refresh_owner = d.pop("refresh_owner", UNSET)

        refresh_instructions = d.pop("refresh_instructions", UNSET)

        evidence_max_age_seconds = d.pop("evidence_max_age_seconds", UNSET)

        operation_business_condition_definition = cls(
            id=id,
            description=description,
            expected_states=expected_states,
            refresh_owner=refresh_owner,
            refresh_instructions=refresh_instructions,
            evidence_max_age_seconds=evidence_max_age_seconds,
        )

        return operation_business_condition_definition
