from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.environment_definition_api_version import (
    EnvironmentDefinitionApiVersion,
    check_environment_definition_api_version,
)
from ..models.environment_definition_queue_pruning_policy import (
    EnvironmentDefinitionQueuePruningPolicy,
    check_environment_definition_queue_pruning_policy,
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
    queue_pruning_policy: EnvironmentDefinitionQueuePruningPolicy | Unset = UNSET
    """Explicit reviewed disposition for removed owned queues when pruning is enabled. Retires admission and
    dispatch while preserving binding IDs, accepted work and delivery receipts. Omit to block queue pruning."""
    configuration: EnvironmentDefinitionConfiguration | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        api_version: str = self.api_version

        project = self.project

        environment = self.environment

        workloads = self.workloads.to_dict()

        queue_pruning_policy: str | Unset = UNSET
        if not isinstance(self.queue_pruning_policy, Unset):
            queue_pruning_policy = self.queue_pruning_policy

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
        if queue_pruning_policy is not UNSET:
            field_dict["queue_pruning_policy"] = queue_pruning_policy
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

        _queue_pruning_policy = d.pop("queue_pruning_policy", UNSET)
        queue_pruning_policy: EnvironmentDefinitionQueuePruningPolicy | Unset
        if isinstance(_queue_pruning_policy, Unset):
            queue_pruning_policy = UNSET
        else:
            queue_pruning_policy = check_environment_definition_queue_pruning_policy(_queue_pruning_policy)

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
            queue_pruning_policy=queue_pruning_policy,
            configuration=configuration,
        )

        return environment_definition
