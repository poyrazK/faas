from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_binding_response_kind import (
    ProjectEnvironmentBindingResponseKind,
    check_project_environment_binding_response_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentBindingResponse")


@_attrs_define
class ProjectEnvironmentBindingResponse:
    """Managed resource binding and its target-scoped credential metadata."""

    kind: ProjectEnvironmentBindingResponseKind
    binding_id: str
    secret_keys: list[str]
    credential_generation: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        binding_id = self.binding_id

        secret_keys = self.secret_keys

        credential_generation = self.credential_generation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "binding_id": binding_id,
                "secret_keys": secret_keys,
            }
        )
        if credential_generation is not UNSET:
            field_dict["credential_generation"] = credential_generation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_project_environment_binding_response_kind(d.pop("kind"))

        binding_id = d.pop("binding_id")

        secret_keys = cast(list[str], d.pop("secret_keys"))

        credential_generation = d.pop("credential_generation", UNSET)

        project_environment_binding_response = cls(
            kind=kind,
            binding_id=binding_id,
            secret_keys=secret_keys,
            credential_generation=credential_generation,
        )

        project_environment_binding_response.additional_properties = d
        return project_environment_binding_response

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
