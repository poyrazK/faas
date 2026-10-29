from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.environment_field_ownership_response_status import (
    EnvironmentFieldOwnershipResponseStatus,
    check_environment_field_ownership_response_status,
)

T = TypeVar("T", bound="EnvironmentFieldOwnershipResponse")


@_attrs_define
class EnvironmentFieldOwnershipResponse:
    """Result of claiming or releasing Terraform ownership of environment fields."""

    status: EnvironmentFieldOwnershipResponseStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_environment_field_ownership_response_status(d.pop("status"))

        environment_field_ownership_response = cls(
            status=status,
        )

        environment_field_ownership_response.additional_properties = d
        return environment_field_ownership_response

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
