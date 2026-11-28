from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="BindingVerificationDifference")


@_attrs_define
class BindingVerificationDifference:
    """Only whitelisted public scalar values; no credentials, provider origins, commands or guest output."""

    field: str
    reviewed: str
    current: str
    app: str | Unset = UNSET
    type_: str | Unset = UNSET
    name: str | Unset = UNSET
    binding: str | Unset = UNSET
    scope: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        field = self.field

        reviewed = self.reviewed

        current = self.current

        app = self.app

        type_ = self.type_

        name = self.name

        binding = self.binding

        scope = self.scope

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "field": field,
                "reviewed": reviewed,
                "current": current,
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        field = d.pop("field")

        reviewed = d.pop("reviewed")

        current = d.pop("current")

        app = d.pop("app", UNSET)

        type_ = d.pop("type", UNSET)

        name = d.pop("name", UNSET)

        binding = d.pop("binding", UNSET)

        scope = d.pop("scope", UNSET)

        binding_verification_difference = cls(
            field=field,
            reviewed=reviewed,
            current=current,
            app=app,
            type_=type_,
            name=name,
            binding=binding,
            scope=scope,
        )

        binding_verification_difference.additional_properties = d
        return binding_verification_difference

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
