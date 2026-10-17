from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.automation_live_suite_case_outcome import (
    AutomationLiveSuiteCaseOutcome,
    check_automation_live_suite_case_outcome,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_live_verification import AutomationLiveVerification


T = TypeVar("T", bound="AutomationLiveSuiteCase")


@_attrs_define
class AutomationLiveSuiteCase:
    """A server-derived case observation; unavailable records keep their original run identity."""

    name: str
    outcome: AutomationLiveSuiteCaseOutcome
    run_id: UUID | Unset = UNSET
    verification: AutomationLiveVerification | Unset = UNSET
    """Safe provenance and status for one explicitly authorized live verification run."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        outcome: str = self.outcome

        run_id: str | Unset = UNSET
        if not isinstance(self.run_id, Unset):
            run_id = str(self.run_id)

        verification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.verification, Unset):
            verification = self.verification.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "outcome": outcome,
            }
        )
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if verification is not UNSET:
            field_dict["verification"] = verification

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_verification import AutomationLiveVerification

        d = dict(src_dict)
        name = d.pop("name")

        outcome = check_automation_live_suite_case_outcome(d.pop("outcome"))

        _run_id = d.pop("run_id", UNSET)
        run_id: UUID | Unset
        if isinstance(_run_id, Unset):
            run_id = UNSET
        else:
            run_id = UUID(_run_id)

        _verification = d.pop("verification", UNSET)
        verification: AutomationLiveVerification | Unset
        if isinstance(_verification, Unset):
            verification = UNSET
        else:
            verification = AutomationLiveVerification.from_dict(_verification)

        automation_live_suite_case = cls(
            name=name,
            outcome=outcome,
            run_id=run_id,
            verification=verification,
        )

        return automation_live_suite_case
