from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PublishExecutionTaskRequest")


@_attrs_define
class PublishExecutionTaskRequest:
    """Source, execution configuration and contracts for a new immutable task version."""

    name: str
    version: int
    definition: Any
    """Source and JSON input for one disposable execution. Send either the
    legacy `source` string or an ephemeral `files` bundle with an
    `entrypoint`. `artifact_inputs` may add files from successful runs in
    the same key family for Runs-only credentials, or from the same account
    for broad credentials; they are copied into the new
    encrypted request and staged only in its ephemeral guest filesystem.
    Optional `workflow_id` and `step_label` values group run receipts in
    control-plane metadata; the guest does not receive them.
    Optional `integration_ids` explicitly select managed integrations
    granted for this Run; the IDs remain control-plane metadata and are
    not delivered to the guest payload.
    v1 supports only the listed interpreter runtimes and
    `network.mode=none`; dependencies, secrets, environment injection, and
    persistent disks are not part of this contract.
    """
    agent_endpoint_ids: list[UUID] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        version = self.version

        definition: Any
        definition = self.definition

        agent_endpoint_ids: list[str] | Unset = UNSET
        if not isinstance(self.agent_endpoint_ids, Unset):
            agent_endpoint_ids = []
            for agent_endpoint_ids_item_data in self.agent_endpoint_ids:
                agent_endpoint_ids_item = str(agent_endpoint_ids_item_data)
                agent_endpoint_ids.append(agent_endpoint_ids_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "version": version,
                "definition": definition,
            }
        )
        if agent_endpoint_ids is not UNSET:
            field_dict["agent_endpoint_ids"] = agent_endpoint_ids

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        version = d.pop("version")

        def _parse_definition(data: object) -> Any:
            return cast(Any, data)

        definition = _parse_definition(d.pop("definition"))

        _agent_endpoint_ids = d.pop("agent_endpoint_ids", UNSET)
        agent_endpoint_ids: list[UUID] | Unset = UNSET
        if _agent_endpoint_ids is not UNSET:
            agent_endpoint_ids = []
            for agent_endpoint_ids_item_data in _agent_endpoint_ids:
                agent_endpoint_ids_item = UUID(agent_endpoint_ids_item_data)

                agent_endpoint_ids.append(agent_endpoint_ids_item)

        publish_execution_task_request = cls(
            name=name,
            version=version,
            definition=definition,
            agent_endpoint_ids=agent_endpoint_ids,
        )

        publish_execution_task_request.additional_properties = d
        return publish_execution_task_request

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
