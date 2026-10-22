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
    `__gregale_service_binding_probe_v2__ <service> <target_uuid>` requires
    verification_deployment_id and qualifies only that exact live target in
    the caller scope. It sends a safe HEAD without waking or invoking the target.
    `__gregale_service_binding_probe_v1__ <service>` is reserved for the
    Gregale HTTPS service-binding canary and is handled by guest-init.
    `__gregale_outbound_binding_probe_v1__ <integration-id>` selects a
    configured outbound probe; the server supplies immutable gateway routing metadata.
    `interactive=true` admits an ADR-1037 session (operator-gated): the
    command defaults to `/bin/sh`, the timeout defaults to 3600 seconds
    and bounds the session, no output is retained, and the create
    response carries a one-time `attach` credential for
    GET /v1/apps/{slug}/tasks/{id}/attach. `tty` allocates a terminal.

    """

    verification_deployment_id: UUID | Unset = UNSET
    """Exact source deployment for a reserved binding verification probe; omit for the current manual-task
    selection."""
    smoke_deployment_id: UUID | Unset = UNSET
    """Exact caller deployment for a reserved service smoke GET; mutually exclusive with
    verification_deployment_id. Omit for automatic caller selection."""
    command: list[str] | Unset = UNSET
    command_shell: bool | Unset = False
    timeout_seconds: int | Unset = UNSET
    """Zero uses the 600-second default."""
    max_output_bytes: int | Unset = UNSET
    """Zero uses the 1 MiB default; non-zero values must be at least 1024. Must be omitted for interactive
    sessions."""
    interactive: bool | Unset = False
    """Admit an interactive session (ADR-1037). The command is required unless interactive."""
    tty: bool | Unset = False
    """Allocate a pseudo-terminal; requires interactive."""

    def to_dict(self) -> dict[str, Any]:
        verification_deployment_id: str | Unset = UNSET
        if not isinstance(self.verification_deployment_id, Unset):
            verification_deployment_id = str(self.verification_deployment_id)

        smoke_deployment_id: str | Unset = UNSET
        if not isinstance(self.smoke_deployment_id, Unset):
            smoke_deployment_id = str(self.smoke_deployment_id)

        command: list[str] | Unset = UNSET
        if not isinstance(self.command, Unset):
            command = self.command

        command_shell = self.command_shell

        timeout_seconds = self.timeout_seconds

        max_output_bytes = self.max_output_bytes

        interactive = self.interactive

        tty = self.tty

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if verification_deployment_id is not UNSET:
            field_dict["verification_deployment_id"] = verification_deployment_id
        if smoke_deployment_id is not UNSET:
            field_dict["smoke_deployment_id"] = smoke_deployment_id
        if command is not UNSET:
            field_dict["command"] = command
        if command_shell is not UNSET:
            field_dict["command_shell"] = command_shell
        if timeout_seconds is not UNSET:
            field_dict["timeout_seconds"] = timeout_seconds
        if max_output_bytes is not UNSET:
            field_dict["max_output_bytes"] = max_output_bytes
        if interactive is not UNSET:
            field_dict["interactive"] = interactive
        if tty is not UNSET:
            field_dict["tty"] = tty

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
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

        command = cast(list[str], d.pop("command", UNSET))

        command_shell = d.pop("command_shell", UNSET)

        timeout_seconds = d.pop("timeout_seconds", UNSET)

        max_output_bytes = d.pop("max_output_bytes", UNSET)

        interactive = d.pop("interactive", UNSET)

        tty = d.pop("tty", UNSET)

        create_app_task_request = cls(
            verification_deployment_id=verification_deployment_id,
            smoke_deployment_id=smoke_deployment_id,
            command=command,
            command_shell=command_shell,
            timeout_seconds=timeout_seconds,
            max_output_bytes=max_output_bytes,
            interactive=interactive,
            tty=tty,
        )

        return create_app_task_request
