from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_planned_decision import OperationWorkflowPlannedDecision
    from ..models.operation_workflow_planned_effect import OperationWorkflowPlannedEffect
    from ..models.operation_workflow_planned_invariant import OperationWorkflowPlannedInvariant


T = TypeVar("T", bound="CheckPlatformTenantSelfWorkflowReadinessBody")


@_attrs_define
class CheckPlatformTenantSelfWorkflowReadinessBody:
    scope: str
    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    workflow: str
    instance_id: str
    operation: str
    from_state: str
    to_state: str
    app_id: UUID
    """For this proposed transition, required in customer-self mode only."""
    effects: list[OperationWorkflowPlannedEffect] | Unset = UNSET
    invariants: list[OperationWorkflowPlannedInvariant] | Unset = UNSET
    decisions: list[OperationWorkflowPlannedDecision] | Unset = UNSET
    tenant_id: UUID | Unset = UNSET
    """For this proposed transition, required in account mode only."""
    milestones: list[str] | Unset = UNSET
    """Planned milestone names for this transition; retained historical facts are not evidence for the new
    transaction."""
    state_revision: int | Unset = UNSET
    """For this proposed transition, optional expected retained source revision."""
    contract_version: int | Unset = UNSET
    """For this proposed transition, optional expected selected contract version."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        subject = self.subject.to_dict()

        workflow = self.workflow

        instance_id = self.instance_id

        operation = self.operation

        from_state = self.from_state

        to_state = self.to_state

        app_id = str(self.app_id)

        effects: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.effects, Unset):
            effects = []
            for effects_item_data in self.effects:
                effects_item = effects_item_data.to_dict()
                effects.append(effects_item)

        invariants: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.invariants, Unset):
            invariants = []
            for invariants_item_data in self.invariants:
                invariants_item = invariants_item_data.to_dict()
                invariants.append(invariants_item)

        decisions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.decisions, Unset):
            decisions = []
            for decisions_item_data in self.decisions:
                decisions_item = decisions_item_data.to_dict()
                decisions.append(decisions_item)

        tenant_id: str | Unset = UNSET
        if not isinstance(self.tenant_id, Unset):
            tenant_id = str(self.tenant_id)

        milestones: list[str] | Unset = UNSET
        if not isinstance(self.milestones, Unset):
            milestones = self.milestones

        state_revision = self.state_revision

        contract_version = self.contract_version

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "subject": subject,
                "workflow": workflow,
                "instance_id": instance_id,
                "operation": operation,
                "from_state": from_state,
                "to_state": to_state,
                "app_id": app_id,
            }
        )
        if effects is not UNSET:
            field_dict["effects"] = effects
        if invariants is not UNSET:
            field_dict["invariants"] = invariants
        if decisions is not UNSET:
            field_dict["decisions"] = decisions
        if tenant_id is not UNSET:
            field_dict["tenant_id"] = tenant_id
        if milestones is not UNSET:
            field_dict["milestones"] = milestones
        if state_revision is not UNSET:
            field_dict["state_revision"] = state_revision
        if contract_version is not UNSET:
            field_dict["contract_version"] = contract_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_planned_decision import OperationWorkflowPlannedDecision
        from ..models.operation_workflow_planned_effect import OperationWorkflowPlannedEffect
        from ..models.operation_workflow_planned_invariant import OperationWorkflowPlannedInvariant

        d = dict(src_dict)
        scope = d.pop("scope")

        subject = OperationSubject.from_dict(d.pop("subject"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        operation = d.pop("operation")

        from_state = d.pop("from_state")

        to_state = d.pop("to_state")

        app_id = UUID(d.pop("app_id"))

        _effects = d.pop("effects", UNSET)
        effects: list[OperationWorkflowPlannedEffect] | Unset = UNSET
        if _effects is not UNSET:
            effects = []
            for effects_item_data in _effects:
                effects_item = OperationWorkflowPlannedEffect.from_dict(effects_item_data)

                effects.append(effects_item)

        _invariants = d.pop("invariants", UNSET)
        invariants: list[OperationWorkflowPlannedInvariant] | Unset = UNSET
        if _invariants is not UNSET:
            invariants = []
            for invariants_item_data in _invariants:
                invariants_item = OperationWorkflowPlannedInvariant.from_dict(invariants_item_data)

                invariants.append(invariants_item)

        _decisions = d.pop("decisions", UNSET)
        decisions: list[OperationWorkflowPlannedDecision] | Unset = UNSET
        if _decisions is not UNSET:
            decisions = []
            for decisions_item_data in _decisions:
                decisions_item = OperationWorkflowPlannedDecision.from_dict(decisions_item_data)

                decisions.append(decisions_item)

        _tenant_id = d.pop("tenant_id", UNSET)
        tenant_id: UUID | Unset
        if isinstance(_tenant_id, Unset):
            tenant_id = UNSET
        else:
            tenant_id = UUID(_tenant_id)

        milestones = cast(list[str], d.pop("milestones", UNSET))

        state_revision = d.pop("state_revision", UNSET)

        contract_version = d.pop("contract_version", UNSET)

        check_platform_tenant_self_workflow_readiness_body = cls(
            scope=scope,
            subject=subject,
            workflow=workflow,
            instance_id=instance_id,
            operation=operation,
            from_state=from_state,
            to_state=to_state,
            app_id=app_id,
            effects=effects,
            invariants=invariants,
            decisions=decisions,
            tenant_id=tenant_id,
            milestones=milestones,
            state_revision=state_revision,
            contract_version=contract_version,
        )

        check_platform_tenant_self_workflow_readiness_body.additional_properties = d
        return check_platform_tenant_self_workflow_readiness_body

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
