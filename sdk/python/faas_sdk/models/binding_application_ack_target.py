from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_application_ack_target_application_ack import (
    BindingApplicationAckTargetApplicationAck,
    check_binding_application_ack_target_application_ack,
)
from ..models.binding_application_ack_target_application_ack_reason import (
    BindingApplicationAckTargetApplicationAckReason,
    check_binding_application_ack_target_application_ack_reason,
)
from ..models.binding_application_ack_target_application_ack_status import (
    BindingApplicationAckTargetApplicationAckStatus,
    check_binding_application_ack_target_application_ack_status,
)
from ..models.binding_application_ack_target_projection import (
    BindingApplicationAckTargetProjection,
    check_binding_application_ack_target_projection,
)
from ..models.binding_application_ack_target_reload_reason import (
    BindingApplicationAckTargetReloadReason,
    check_binding_application_ack_target_reload_reason,
)
from ..models.binding_application_ack_target_reload_status import (
    BindingApplicationAckTargetReloadStatus,
    check_binding_application_ack_target_reload_status,
)
from ..models.binding_application_ack_target_reload_support import (
    BindingApplicationAckTargetReloadSupport,
    check_binding_application_ack_target_reload_support,
)
from ..models.binding_application_ack_target_signal import (
    BindingApplicationAckTargetSignal,
    check_binding_application_ack_target_signal,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="BindingApplicationAckTarget")


@_attrs_define
class BindingApplicationAckTarget:
    """Authorized resident workload and managed secret metadata, with independently versioned reload and application
    observations. Contains no credential values, hashes or private binding IDs. Empty workload_name means the main
    workload.

    """

    deployment_id: UUID
    instance_id: UUID
    runtime_state: str
    key: str
    reload_support: BindingApplicationAckTargetReloadSupport
    current_version: int
    reload_version: int
    application_ack_version: int
    workload_name: str | Unset = UNSET
    projection: BindingApplicationAckTargetProjection | Unset = UNSET
    signal: BindingApplicationAckTargetSignal | Unset = UNSET
    reload_at: datetime.datetime | Unset = UNSET
    application_ack: BindingApplicationAckTargetApplicationAck | Unset = UNSET
    application_ack_at: datetime.datetime | Unset = UNSET
    process_generation: str | Unset = UNSET
    """Active execution identity; absent for legacy or retired processes."""
    application_ack_generation: str | Unset = UNSET
    """Execution identity supplied in the application ACK; strict adoption requires it to match process_generation."""
    reload_status: BindingApplicationAckTargetReloadStatus | Unset = UNSET
    """Derived status for this workload and its secret projection and notification."""
    reload_reason: BindingApplicationAckTargetReloadReason | Unset = UNSET
    """Stable sanitized reason for reload_status; contains no raw errors or secret data."""
    application_ack_status: BindingApplicationAckTargetApplicationAckStatus | Unset = UNSET
    """Derived status for this workload and its application acknowledgement, including process-generation fencing."""
    application_ack_reason: BindingApplicationAckTargetApplicationAckReason | Unset = UNSET
    """Stable sanitized reason for application_ack_status; contains no generation values, raw errors or secret
    data."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        instance_id = str(self.instance_id)

        runtime_state = self.runtime_state

        key = self.key

        reload_support: str = self.reload_support

        current_version = self.current_version

        reload_version = self.reload_version

        application_ack_version = self.application_ack_version

        workload_name = self.workload_name

        projection: str | Unset = UNSET
        if not isinstance(self.projection, Unset):
            projection = self.projection

        signal: str | Unset = UNSET
        if not isinstance(self.signal, Unset):
            signal = self.signal

        reload_at: str | Unset = UNSET
        if not isinstance(self.reload_at, Unset):
            reload_at = self.reload_at.isoformat()

        application_ack: str | Unset = UNSET
        if not isinstance(self.application_ack, Unset):
            application_ack = self.application_ack

        application_ack_at: str | Unset = UNSET
        if not isinstance(self.application_ack_at, Unset):
            application_ack_at = self.application_ack_at.isoformat()

        process_generation = self.process_generation

        application_ack_generation = self.application_ack_generation

        reload_status: str | Unset = UNSET
        if not isinstance(self.reload_status, Unset):
            reload_status = self.reload_status

        reload_reason: str | Unset = UNSET
        if not isinstance(self.reload_reason, Unset):
            reload_reason = self.reload_reason

        application_ack_status: str | Unset = UNSET
        if not isinstance(self.application_ack_status, Unset):
            application_ack_status = self.application_ack_status

        application_ack_reason: str | Unset = UNSET
        if not isinstance(self.application_ack_reason, Unset):
            application_ack_reason = self.application_ack_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "instance_id": instance_id,
                "runtime_state": runtime_state,
                "key": key,
                "reload_support": reload_support,
                "current_version": current_version,
                "reload_version": reload_version,
                "application_ack_version": application_ack_version,
            }
        )
        if workload_name is not UNSET:
            field_dict["workload_name"] = workload_name
        if projection is not UNSET:
            field_dict["projection"] = projection
        if signal is not UNSET:
            field_dict["signal"] = signal
        if reload_at is not UNSET:
            field_dict["reload_at"] = reload_at
        if application_ack is not UNSET:
            field_dict["application_ack"] = application_ack
        if application_ack_at is not UNSET:
            field_dict["application_ack_at"] = application_ack_at
        if process_generation is not UNSET:
            field_dict["process_generation"] = process_generation
        if application_ack_generation is not UNSET:
            field_dict["application_ack_generation"] = application_ack_generation
        if reload_status is not UNSET:
            field_dict["reload_status"] = reload_status
        if reload_reason is not UNSET:
            field_dict["reload_reason"] = reload_reason
        if application_ack_status is not UNSET:
            field_dict["application_ack_status"] = application_ack_status
        if application_ack_reason is not UNSET:
            field_dict["application_ack_reason"] = application_ack_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        instance_id = UUID(d.pop("instance_id"))

        runtime_state = d.pop("runtime_state")

        key = d.pop("key")

        reload_support = check_binding_application_ack_target_reload_support(d.pop("reload_support"))

        current_version = d.pop("current_version")

        reload_version = d.pop("reload_version")

        application_ack_version = d.pop("application_ack_version")

        workload_name = d.pop("workload_name", UNSET)

        _projection = d.pop("projection", UNSET)
        projection: BindingApplicationAckTargetProjection | Unset
        if isinstance(_projection, Unset):
            projection = UNSET
        else:
            projection = check_binding_application_ack_target_projection(_projection)

        _signal = d.pop("signal", UNSET)
        signal: BindingApplicationAckTargetSignal | Unset
        if isinstance(_signal, Unset):
            signal = UNSET
        else:
            signal = check_binding_application_ack_target_signal(_signal)

        _reload_at = d.pop("reload_at", UNSET)
        reload_at: datetime.datetime | Unset
        if isinstance(_reload_at, Unset):
            reload_at = UNSET
        else:
            reload_at = datetime.datetime.fromisoformat(_reload_at)

        _application_ack = d.pop("application_ack", UNSET)
        application_ack: BindingApplicationAckTargetApplicationAck | Unset
        if isinstance(_application_ack, Unset):
            application_ack = UNSET
        else:
            application_ack = check_binding_application_ack_target_application_ack(_application_ack)

        _application_ack_at = d.pop("application_ack_at", UNSET)
        application_ack_at: datetime.datetime | Unset
        if isinstance(_application_ack_at, Unset):
            application_ack_at = UNSET
        else:
            application_ack_at = datetime.datetime.fromisoformat(_application_ack_at)

        process_generation = d.pop("process_generation", UNSET)

        application_ack_generation = d.pop("application_ack_generation", UNSET)

        _reload_status = d.pop("reload_status", UNSET)
        reload_status: BindingApplicationAckTargetReloadStatus | Unset
        if isinstance(_reload_status, Unset):
            reload_status = UNSET
        else:
            reload_status = check_binding_application_ack_target_reload_status(_reload_status)

        _reload_reason = d.pop("reload_reason", UNSET)
        reload_reason: BindingApplicationAckTargetReloadReason | Unset
        if isinstance(_reload_reason, Unset):
            reload_reason = UNSET
        else:
            reload_reason = check_binding_application_ack_target_reload_reason(_reload_reason)

        _application_ack_status = d.pop("application_ack_status", UNSET)
        application_ack_status: BindingApplicationAckTargetApplicationAckStatus | Unset
        if isinstance(_application_ack_status, Unset):
            application_ack_status = UNSET
        else:
            application_ack_status = check_binding_application_ack_target_application_ack_status(
                _application_ack_status
            )

        _application_ack_reason = d.pop("application_ack_reason", UNSET)
        application_ack_reason: BindingApplicationAckTargetApplicationAckReason | Unset
        if isinstance(_application_ack_reason, Unset):
            application_ack_reason = UNSET
        else:
            application_ack_reason = check_binding_application_ack_target_application_ack_reason(
                _application_ack_reason
            )

        binding_application_ack_target = cls(
            deployment_id=deployment_id,
            instance_id=instance_id,
            runtime_state=runtime_state,
            key=key,
            reload_support=reload_support,
            current_version=current_version,
            reload_version=reload_version,
            application_ack_version=application_ack_version,
            workload_name=workload_name,
            projection=projection,
            signal=signal,
            reload_at=reload_at,
            application_ack=application_ack,
            application_ack_at=application_ack_at,
            process_generation=process_generation,
            application_ack_generation=application_ack_generation,
            reload_status=reload_status,
            reload_reason=reload_reason,
            application_ack_status=application_ack_status,
            application_ack_reason=application_ack_reason,
        )

        binding_application_ack_target.additional_properties = d
        return binding_application_ack_target

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
