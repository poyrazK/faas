from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_app_task_request import CreateAppTaskRequest


T = TypeVar("T", bound="ExclusiveAppTaskOperationRequest")


@_attrs_define
class ExclusiveAppTaskOperationRequest:
    """Deployment-attached app task intent admitted under a named exclusive-operation policy."""

    policy: str
    key: bool | float | str
    """Business identifier for this deployment-attached task lane; account and customer authorization scope comes
    from trusted platform context."""
    task: CreateAppTaskRequest
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
    equivalence_key: str | Unset = UNSET
    """Optional identity for joining accepted app tasks only when their task intents are equivalent."""

    def to_dict(self) -> dict[str, Any]:
        policy = self.policy

        key: bool | float | str
        key = self.key

        task = self.task.to_dict()

        equivalence_key = self.equivalence_key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "policy": policy,
                "key": key,
                "task": task,
            }
        )
        if equivalence_key is not UNSET:
            field_dict["equivalence_key"] = equivalence_key

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_app_task_request import CreateAppTaskRequest

        d = dict(src_dict)
        policy = d.pop("policy")

        def _parse_key(data: object) -> bool | float | str:
            return cast(bool | float | str, data)

        key = _parse_key(d.pop("key"))

        task = CreateAppTaskRequest.from_dict(d.pop("task"))

        equivalence_key = d.pop("equivalence_key", UNSET)

        exclusive_app_task_operation_request = cls(
            policy=policy,
            key=key,
            task=task,
            equivalence_key=equivalence_key,
        )

        return exclusive_app_task_operation_request
