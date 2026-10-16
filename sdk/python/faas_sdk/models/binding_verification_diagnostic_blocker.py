from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_verification_diagnostic_blocker_recovery import (
    BindingVerificationDiagnosticBlockerRecovery,
    check_binding_verification_diagnostic_blocker_recovery,
)
from ..models.binding_verification_diagnostic_blocker_resource_kind import (
    BindingVerificationDiagnosticBlockerResourceKind,
    check_binding_verification_diagnostic_blocker_resource_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="BindingVerificationDiagnosticBlocker")


@_attrs_define
class BindingVerificationDiagnosticBlocker:
    """Public reason and recovery classification for a blocked verification selection."""

    code: str
    message: str
    recovery: BindingVerificationDiagnosticBlockerRecovery
    app: str | Unset = UNSET
    type_: str | Unset = UNSET
    name: str | Unset = UNSET
    binding: str | Unset = UNSET
    scope: str | Unset = UNSET
    status: str | Unset = UNSET
    step_index: int | Unset = UNSET
    resource_id: UUID | Unset = UNSET
    resource_kind: BindingVerificationDiagnosticBlockerResourceKind | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        message = self.message

        recovery: str = self.recovery

        app = self.app

        type_ = self.type_

        name = self.name

        binding = self.binding

        scope = self.scope

        status = self.status

        step_index = self.step_index

        resource_id: str | Unset = UNSET
        if not isinstance(self.resource_id, Unset):
            resource_id = str(self.resource_id)

        resource_kind: str | Unset = UNSET
        if not isinstance(self.resource_kind, Unset):
            resource_kind = self.resource_kind

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "message": message,
                "recovery": recovery,
            }
        )
        if app is not UNSET:
            field_dict["app"] = app
        if type_ is not UNSET:
            field_dict["type"] = type_
        if name is not UNSET:
            field_dict["name"] = name
        if binding is not UNSET:
            field_dict["binding"] = binding
        if scope is not UNSET:
            field_dict["scope"] = scope
        if status is not UNSET:
            field_dict["status"] = status
        if step_index is not UNSET:
            field_dict["step_index"] = step_index
        if resource_id is not UNSET:
            field_dict["resource_id"] = resource_id
        if resource_kind is not UNSET:
            field_dict["resource_kind"] = resource_kind

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        message = d.pop("message")

        recovery = check_binding_verification_diagnostic_blocker_recovery(d.pop("recovery"))

        app = d.pop("app", UNSET)

        type_ = d.pop("type", UNSET)

        name = d.pop("name", UNSET)

        binding = d.pop("binding", UNSET)

        scope = d.pop("scope", UNSET)

        status = d.pop("status", UNSET)

        step_index = d.pop("step_index", UNSET)

        _resource_id = d.pop("resource_id", UNSET)
        resource_id: UUID | Unset
        if isinstance(_resource_id, Unset):
            resource_id = UNSET
        else:
            resource_id = UUID(_resource_id)

        _resource_kind = d.pop("resource_kind", UNSET)
        resource_kind: BindingVerificationDiagnosticBlockerResourceKind | Unset
        if isinstance(_resource_kind, Unset):
            resource_kind = UNSET
        else:
            resource_kind = check_binding_verification_diagnostic_blocker_resource_kind(_resource_kind)

        binding_verification_diagnostic_blocker = cls(
            code=code,
            message=message,
            recovery=recovery,
            app=app,
            type_=type_,
            name=name,
            binding=binding,
            scope=scope,
            status=status,
            step_index=step_index,
            resource_id=resource_id,
            resource_kind=resource_kind,
        )

        binding_verification_diagnostic_blocker.additional_properties = d
        return binding_verification_diagnostic_blocker

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
