from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.stored_workflow_check_exclusion import StoredWorkflowCheckExclusion
    from ..models.stored_workflow_check_scenario import StoredWorkflowCheckScenario


T = TypeVar("T", bound="StoredWorkflowCheckEvidence")


@_attrs_define
class StoredWorkflowCheckEvidence:
    """Simulation check metadata. server_verified is set only for server-issued publishing receipts; legacy or plain client
    evidence is unverified. Bound to the saved draft hash and version. Contains metadata only; no sample inputs,
    outputs, or failure text. Checked time must be within the past day (five minutes of future clock skew allowed).

    """

    definition_hash: str
    checked_version: int
    checked_at: datetime.datetime
    scenarios: list[StoredWorkflowCheckScenario]
    coverage_required: bool
    coverage_passed: bool
    """True when coverage_remaining is zero; required coverage must pass before publishing."""
    coverage_remaining: int
    exclusions: list[StoredWorkflowCheckExclusion]
    server_verified: bool | Unset = UNSET
    """True only when publication used a stored server-issued receipt. Clients cannot attest to this flag."""

    def to_dict(self) -> dict[str, Any]:
        definition_hash = self.definition_hash

        checked_version = self.checked_version

        checked_at = self.checked_at.isoformat()

        scenarios = []
        for scenarios_item_data in self.scenarios:
            scenarios_item = scenarios_item_data.to_dict()
            scenarios.append(scenarios_item)

        coverage_required = self.coverage_required

        coverage_passed = self.coverage_passed

        coverage_remaining = self.coverage_remaining

        exclusions = []
        for exclusions_item_data in self.exclusions:
            exclusions_item = exclusions_item_data.to_dict()
            exclusions.append(exclusions_item)

        server_verified = self.server_verified

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "definition_hash": definition_hash,
                "checked_version": checked_version,
                "checked_at": checked_at,
                "scenarios": scenarios,
                "coverage_required": coverage_required,
                "coverage_passed": coverage_passed,
                "coverage_remaining": coverage_remaining,
                "exclusions": exclusions,
            }
        )
        if server_verified is not UNSET:
            field_dict["server_verified"] = server_verified

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.stored_workflow_check_exclusion import StoredWorkflowCheckExclusion
        from ..models.stored_workflow_check_scenario import StoredWorkflowCheckScenario

        d = dict(src_dict)
        definition_hash = d.pop("definition_hash")

        checked_version = d.pop("checked_version")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        scenarios = []
        _scenarios = d.pop("scenarios")
        for scenarios_item_data in _scenarios:
            scenarios_item = StoredWorkflowCheckScenario.from_dict(scenarios_item_data)

            scenarios.append(scenarios_item)

        coverage_required = d.pop("coverage_required")

        coverage_passed = d.pop("coverage_passed")

        coverage_remaining = d.pop("coverage_remaining")

        exclusions = []
        _exclusions = d.pop("exclusions")
        for exclusions_item_data in _exclusions:
            exclusions_item = StoredWorkflowCheckExclusion.from_dict(exclusions_item_data)

            exclusions.append(exclusions_item)

        server_verified = d.pop("server_verified", UNSET)

        stored_workflow_check_evidence = cls(
            definition_hash=definition_hash,
            checked_version=checked_version,
            checked_at=checked_at,
            scenarios=scenarios,
            coverage_required=coverage_required,
            coverage_passed=coverage_passed,
            coverage_remaining=coverage_remaining,
            exclusions=exclusions,
            server_verified=server_verified,
        )

        return stored_workflow_check_evidence
