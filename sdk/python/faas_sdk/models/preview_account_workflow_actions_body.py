from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject


T = TypeVar("T", bound="PreviewAccountWorkflowActionsBody")


@_attrs_define
class PreviewAccountWorkflowActionsBody:
    scope: str
    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    workflow: str
    instance_id: str
    tenant_id: UUID
    """Required in account mode only."""
    app_id: UUID | Unset = UNSET
    """Required in customer-self mode only."""
    operation: str | Unset = UNSET
    state_revision: int | Unset = UNSET
    """Optional expected retained source revision."""
    contract_version: int | Unset = UNSET
    """Optional expected selected contract version."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        subject = self.subject.to_dict()

        workflow = self.workflow

        instance_id = self.instance_id

        tenant_id = str(self.tenant_id)

        app_id: str | Unset = UNSET
        if not isinstance(self.app_id, Unset):
            app_id = str(self.app_id)

        operation = self.operation

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
                "tenant_id": tenant_id,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if operation is not UNSET:
            field_dict["operation"] = operation
        if state_revision is not UNSET:
            field_dict["state_revision"] = state_revision
        if contract_version is not UNSET:
            field_dict["contract_version"] = contract_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject

        d = dict(src_dict)
        scope = d.pop("scope")

        subject = OperationSubject.from_dict(d.pop("subject"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        tenant_id = UUID(d.pop("tenant_id"))

        _app_id = d.pop("app_id", UNSET)
        app_id: UUID | Unset
        if isinstance(_app_id, Unset):
            app_id = UNSET
        else:
            app_id = UUID(_app_id)

        operation = d.pop("operation", UNSET)

        state_revision = d.pop("state_revision", UNSET)

        contract_version = d.pop("contract_version", UNSET)

        preview_account_workflow_actions_body = cls(
            scope=scope,
            subject=subject,
            workflow=workflow,
            instance_id=instance_id,
            tenant_id=tenant_id,
            app_id=app_id,
            operation=operation,
            state_revision=state_revision,
            contract_version=contract_version,
        )

        preview_account_workflow_actions_body.additional_properties = d
        return preview_account_workflow_actions_body

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
