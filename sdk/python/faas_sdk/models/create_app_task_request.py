from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAppTaskRequest")


@_attrs_define
class CreateAppTaskRequest:
    """One manual command to execute against the app's live deployment.
    verification_deployment_id optionally selects an exact app-owned,
    materialized live deployment for the reserved service, PostgreSQL,
    object-storage or configured outbound verification probes only.
    smoke_deployment_id selects an exact app-owned, materialized live caller
    deployment for the reserved service smoke GET command. The service must
    be declared on the caller; target authorization remains enforced by the gateway.
    Generic commands cannot select a deployment. The selectors are mutually
    exclusive and require the selected deployment to remain live at atomic
    task admission. An explicit verification probe also requires authorized,
    managed binding metadata. Both selectors are supported only on direct
    POST /v1/apps/{slug}/tasks; exclusive-operation task admission rejects them.
    `command_shell=false` executes argv directly. Shell mode requires one
    command string and is explicit so clients preserve quoting semantics.
    `__gregale_service_binding_probe_v1__ <service>` is reserved for the
    Gregale HTTPS service-binding canary and is handled by guest-init.
    `__gregale_outbound_binding_probe_v1__ <integration-id>` selects a
    configured outbound probe; the server supplies immutable gateway routing metadata.

    """

    command: list[str]
    verification_deployment_id: UUID | Unset = UNSET
    """Exact source deployment for a reserved binding verification probe; omit for the current manual-task
    selection."""
    smoke_deployment_id: UUID | Unset = UNSET
    """Exact caller deployment for a reserved service smoke GET; mutually exclusive with
    verification_deployment_id. Omit for automatic caller selection."""
    command_shell: bool | Unset = False
    timeout_seconds: int | Unset = UNSET
    """Zero uses the 600-second default."""
    max_output_bytes: int | Unset = UNSET
    """Zero uses the 1 MiB default; non-zero values must be at least 1024."""

    def to_dict(self) -> dict[str, Any]:
        command = self.command

        verification_deployment_id: str | Unset = UNSET
        if not isinstance(self.verification_deployment_id, Unset):
            verification_deployment_id = str(self.verification_deployment_id)

        smoke_deployment_id: str | Unset = UNSET
        if not isinstance(self.smoke_deployment_id, Unset):
            smoke_deployment_id = str(self.smoke_deployment_id)

        command_shell = self.command_shell

        timeout_seconds = self.timeout_seconds

        max_output_bytes = self.max_output_bytes

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "command": command,
            }
        )
        if verification_deployment_id is not UNSET:
            field_dict["verification_deployment_id"] = verification_deployment_id
        if smoke_deployment_id is not UNSET:
            field_dict["smoke_deployment_id"] = smoke_deployment_id
        if command_shell is not UNSET:
            field_dict["command_shell"] = command_shell
        if timeout_seconds is not UNSET:
            field_dict["timeout_seconds"] = timeout_seconds
        if max_output_bytes is not UNSET:
            field_dict["max_output_bytes"] = max_output_bytes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        command = cast(list[str], d.pop("command"))

        _verification_deployment_id = d.pop("verification_deployment_id", UNSET)
        verification_deployment_id: UUID | Unset
        if isinstance(_verification_deployment_id, Unset):
            verification_deployment_id = UNSET
        else:
            verification_deployment_id = UUID(_verification_deployment_id)

        _smoke_deployment_id = d.pop("smoke_deployment_id", UNSET)
        smoke_deployment_id: UUID | Unset
        if isinstance(_smoke_deployment_id, Unset):
            smoke_deployment_id = UNSET
        else:
            smoke_deployment_id = UUID(_smoke_deployment_id)

        command_shell = d.pop("command_shell", UNSET)

        timeout_seconds = d.pop("timeout_seconds", UNSET)

        max_output_bytes = d.pop("max_output_bytes", UNSET)

        create_app_task_request = cls(
            command=command,
            verification_deployment_id=verification_deployment_id,
            smoke_deployment_id=smoke_deployment_id,
            command_shell=command_shell,
            timeout_seconds=timeout_seconds,
            max_output_bytes=max_output_bytes,
        )

        return create_app_task_request
