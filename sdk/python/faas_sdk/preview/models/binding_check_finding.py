from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="BindingCheckFinding")


@_attrs_define
class BindingCheckFinding:
    """Stable, sanitized blocker or warning for the bindings policy."""

    code: str
    message: str
    type_: str | Unset = UNSET
    name: str | Unset = UNSET
    binding: str | Unset = UNSET
    scope: str | Unset = UNSET
    deployment_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        message = self.message

        type_ = self.type_

        name = self.name

        binding = self.binding

        scope = self.scope

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "message": message,
            }
        )
        if type_ is not UNSET:
            field_dict["type"] = type_
        if name is not UNSET:
            field_dict["name"] = name
        if binding is not UNSET:
            field_dict["binding"] = binding
        if scope is not UNSET:
            field_dict["scope"] = scope
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        message = d.pop("message")

        type_ = d.pop("type", UNSET)

        name = d.pop("name", UNSET)

        binding = d.pop("binding", UNSET)

        scope = d.pop("scope", UNSET)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        binding_check_finding = cls(
            code=code,
            message=message,
            type_=type_,
            name=name,
            binding=binding,
            scope=scope,
            deployment_id=deployment_id,
        )

        binding_check_finding.additional_properties = d
        return binding_check_finding

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
