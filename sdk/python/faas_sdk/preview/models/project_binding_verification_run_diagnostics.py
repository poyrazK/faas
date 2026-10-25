from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_binding_verification_run_diagnostics_status import (
    ProjectBindingVerificationRunDiagnosticsStatus,
    check_project_binding_verification_run_diagnostics_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_verification_budget import BindingVerificationBudget
    from ..models.binding_verification_diagnostic_blocker import BindingVerificationDiagnosticBlocker
    from ..models.binding_verification_difference import BindingVerificationDifference
    from ..models.binding_verification_recovery_action import BindingVerificationRecoveryAction
    from ..models.project_binding_verification_run_diagnostics_members import (
        ProjectBindingVerificationRunDiagnosticsMembers,
    )


T = TypeVar("T", bound="ProjectBindingVerificationRunDiagnostics")


@_attrs_define
class ProjectBindingVerificationRunDiagnostics:
    """Public graph receipt observations, safe differences and suggested recovery actions."""

    run_id: UUID
    project: str
    environment: str
    status: ProjectBindingVerificationRunDiagnosticsStatus
    checked_at: datetime.datetime
    expires_at: datetime.datetime
    complete: bool
    members: ProjectBindingVerificationRunDiagnosticsMembers
    blockers: list[BindingVerificationDiagnosticBlocker]
    changes: list[BindingVerificationDifference]
    recovery: list[BindingVerificationRecoveryAction]
    budget: BindingVerificationBudget | Unset = UNSET
    """Public observation of shared capacity. Underlying unfinished resources remain counted after diagnostic
    expiry or cancellation requests. No admission authority."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        project = self.project

        environment = self.environment

        status: str = self.status

        checked_at = self.checked_at.isoformat()

        expires_at = self.expires_at.isoformat()

        complete = self.complete

        members = self.members.to_dict()

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item = blockers_item_data.to_dict()
            blockers.append(blockers_item)

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        recovery = []
        for recovery_item_data in self.recovery:
            recovery_item = recovery_item_data.to_dict()
            recovery.append(recovery_item)

        budget: dict[str, Any] | Unset = UNSET
        if not isinstance(self.budget, Unset):
            budget = self.budget.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run_id": run_id,
                "project": project,
                "environment": environment,
                "status": status,
                "checked_at": checked_at,
                "expires_at": expires_at,
                "complete": complete,
                "members": members,
                "blockers": blockers,
                "changes": changes,
                "recovery": recovery,
            }
        )
        if budget is not UNSET:
            field_dict["budget"] = budget

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_verification_budget import BindingVerificationBudget
        from ..models.binding_verification_diagnostic_blocker import BindingVerificationDiagnosticBlocker
        from ..models.binding_verification_difference import BindingVerificationDifference
        from ..models.binding_verification_recovery_action import BindingVerificationRecoveryAction
        from ..models.project_binding_verification_run_diagnostics_members import (
            ProjectBindingVerificationRunDiagnosticsMembers,
        )

        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        project = d.pop("project")

        environment = d.pop("environment")

        status = check_project_binding_verification_run_diagnostics_status(d.pop("status"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        complete = d.pop("complete")

        members = ProjectBindingVerificationRunDiagnosticsMembers.from_dict(d.pop("members"))

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = BindingVerificationDiagnosticBlocker.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = BindingVerificationDifference.from_dict(changes_item_data)

            changes.append(changes_item)

        recovery = []
        _recovery = d.pop("recovery")
        for recovery_item_data in _recovery:
            recovery_item = BindingVerificationRecoveryAction.from_dict(recovery_item_data)

            recovery.append(recovery_item)

        _budget = d.pop("budget", UNSET)
        budget: BindingVerificationBudget | Unset
        if isinstance(_budget, Unset):
            budget = UNSET
        else:
            budget = BindingVerificationBudget.from_dict(_budget)

        project_binding_verification_run_diagnostics = cls(
            run_id=run_id,
            project=project,
            environment=environment,
            status=status,
            checked_at=checked_at,
            expires_at=expires_at,
            complete=complete,
            members=members,
            blockers=blockers,
            changes=changes,
            recovery=recovery,
            budget=budget,
        )

        project_binding_verification_run_diagnostics.additional_properties = d
        return project_binding_verification_run_diagnostics

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
