from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.environment_definition_api_version import (
    EnvironmentDefinitionApiVersion,
    check_environment_definition_api_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_definition_configuration import EnvironmentDefinitionConfiguration
    from ..models.environment_definition_workloads import EnvironmentDefinitionWorkloads


T = TypeVar("T", bound="EnvironmentDefinition")


@_attrs_define
class EnvironmentDefinition:
    """Versioned Git intent; omitted settings are unmanaged and removal requires explicit pruning. The workloads map is
    required; an explicit empty map represents an empty environment.

    """

    api_version: EnvironmentDefinitionApiVersion
    project: str
    environment: str
    workloads: EnvironmentDefinitionWorkloads
    configuration: EnvironmentDefinitionConfiguration | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        api_version: str = self.api_version

        project = self.project

        environment = self.environment

        workloads = self.workloads.to_dict()

        configuration: dict[str, Any] | Unset = UNSET
        if not isinstance(self.configuration, Unset):
            configuration = self.configuration.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "api_version": api_version,
                "project": project,
                "environment": environment,
                "workloads": workloads,
            }
        )
        if configuration is not UNSET:
            field_dict["configuration"] = configuration

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_definition_configuration import EnvironmentDefinitionConfiguration
        from ..models.environment_definition_workloads import EnvironmentDefinitionWorkloads

        d = dict(src_dict)
        api_version = check_environment_definition_api_version(d.pop("api_version"))

        project = d.pop("project")

        environment = d.pop("environment")

        workloads = EnvironmentDefinitionWorkloads.from_dict(d.pop("workloads"))

        _configuration = d.pop("configuration", UNSET)
        configuration: EnvironmentDefinitionConfiguration | Unset
        if isinstance(_configuration, Unset):
            configuration = UNSET
        else:
            configuration = EnvironmentDefinitionConfiguration.from_dict(_configuration)

        environment_definition = cls(
            api_version=api_version,
            project=project,
            environment=environment,
            workloads=workloads,
            configuration=configuration,
        )

        return environment_definition
