from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.binding_verification_qualification_condition_code import (
    BindingVerificationQualificationConditionCode,
    check_binding_verification_qualification_condition_code,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="BindingVerificationQualificationCondition")


@_attrs_define
class BindingVerificationQualificationCondition:
    """Temporary readiness condition preventing final qualification of a selected binding."""

    app: str
    code: BindingVerificationQualificationConditionCode
    type_: str | Unset = UNSET
    name: str | Unset = UNSET
    binding: str | Unset = UNSET
    scope: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        code: str = self.code

        type_ = self.type_

        name = self.name

        binding = self.binding

        scope = self.scope

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app": app,
                "code": code,
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app = d.pop("app")

        code = check_binding_verification_qualification_condition_code(d.pop("code"))

        type_ = d.pop("type", UNSET)

        name = d.pop("name", UNSET)

        binding = d.pop("binding", UNSET)

        scope = d.pop("scope", UNSET)

        binding_verification_qualification_condition = cls(
            app=app,
            code=code,
            type_=type_,
            name=name,
            binding=binding,
            scope=scope,
        )

        return binding_verification_qualification_condition
