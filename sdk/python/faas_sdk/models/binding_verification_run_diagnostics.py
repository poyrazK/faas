from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_verification_run_diagnostics_status import (
    BindingVerificationRunDiagnosticsStatus,
    check_binding_verification_run_diagnostics_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_verification_budget import BindingVerificationBudget
    from ..models.binding_verification_diagnostic_blocker import BindingVerificationDiagnosticBlocker
    from ..models.binding_verification_difference import BindingVerificationDifference
    from ..models.binding_verification_recovery_action import BindingVerificationRecoveryAction


T = TypeVar("T", bound="BindingVerificationRunDiagnostics")


@_attrs_define
class BindingVerificationRunDiagnostics:
    """Public app receipt observations and recovery suggestions without admission authority."""

    run_id: UUID
    app: str
    deployment_id: UUID
    scope: str
    status: BindingVerificationRunDiagnosticsStatus
    checked_at: datetime.datetime
    expires_at: datetime.datetime
    complete: bool
    """All applicable diagnostic reads succeeded; does not authorize admission."""
    blockers: list[BindingVerificationDiagnosticBlocker]
    changes: list[BindingVerificationDifference]
    recovery: list[BindingVerificationRecoveryAction]
    budget: BindingVerificationBudget | Unset = UNSET
    """Public observation of shared capacity. Underlying unfinished resources remain counted after diagnostic
    expiry or cancellation requests. No admission authority."""
    graph_run_id: UUID | Unset = UNSET
    check_passed: bool | Unset = UNSET
    """Current public evidence satisfies current policy; does not confirm receipt completion."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        app = self.app

        deployment_id = str(self.deployment_id)

        scope = self.scope

        status: str = self.status

        checked_at = self.checked_at.isoformat()

        expires_at = self.expires_at.isoformat()

        complete = self.complete

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

        graph_run_id: str | Unset = UNSET
        if not isinstance(self.graph_run_id, Unset):
            graph_run_id = str(self.graph_run_id)

        check_passed = self.check_passed

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run_id": run_id,
                "app": app,
                "deployment_id": deployment_id,
                "scope": scope,
                "status": status,
                "checked_at": checked_at,
                "expires_at": expires_at,
                "complete": complete,
                "blockers": blockers,
                "changes": changes,
                "recovery": recovery,
            }
        )
        if budget is not UNSET:
            field_dict["budget"] = budget
        if graph_run_id is not UNSET:
            field_dict["graph_run_id"] = graph_run_id
        if check_passed is not UNSET:
            field_dict["check_passed"] = check_passed

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_verification_budget import BindingVerificationBudget
        from ..models.binding_verification_diagnostic_blocker import BindingVerificationDiagnosticBlocker
        from ..models.binding_verification_difference import BindingVerificationDifference
        from ..models.binding_verification_recovery_action import BindingVerificationRecoveryAction

        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        app = d.pop("app")

        deployment_id = UUID(d.pop("deployment_id"))

        scope = d.pop("scope")

        status = check_binding_verification_run_diagnostics_status(d.pop("status"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        complete = d.pop("complete")

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

        _graph_run_id = d.pop("graph_run_id", UNSET)
        graph_run_id: UUID | Unset
        if isinstance(_graph_run_id, Unset):
            graph_run_id = UNSET
        else:
            graph_run_id = UUID(_graph_run_id)

        check_passed = d.pop("check_passed", UNSET)

        binding_verification_run_diagnostics = cls(
            run_id=run_id,
            app=app,
            deployment_id=deployment_id,
            scope=scope,
            status=status,
            checked_at=checked_at,
            expires_at=expires_at,
            complete=complete,
            blockers=blockers,
            changes=changes,
            recovery=recovery,
            budget=budget,
            graph_run_id=graph_run_id,
            check_passed=check_passed,
        )

        binding_verification_run_diagnostics.additional_properties = d
        return binding_verification_run_diagnostics

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
