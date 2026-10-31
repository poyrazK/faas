from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.update_project_environment_config_request_values import UpdateProjectEnvironmentConfigRequestValues


T = TypeVar("T", bound="UpdateProjectEnvironmentConfigRequest")


@_attrs_define
class UpdateProjectEnvironmentConfigRequest:
    """Non-secret JSON configuration to append as a new environment version."""

    values: UpdateProjectEnvironmentConfigRequestValues

    def to_dict(self) -> dict[str, Any]:
        values = self.values.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "values": values,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.update_project_environment_config_request_values import (
            UpdateProjectEnvironmentConfigRequestValues,
        )

        d = dict(src_dict)
        values = UpdateProjectEnvironmentConfigRequestValues.from_dict(d.pop("values"))

        update_project_environment_config_request = cls(
            values=values,
        )

        return update_project_environment_config_request
