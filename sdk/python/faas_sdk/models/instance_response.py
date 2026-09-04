from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.instance_response_execution_mode_type_1 import (
    InstanceResponseExecutionModeType1,
    check_instance_response_execution_mode_type_1,
)
from ..models.instance_response_execution_mode_type_2_type_1 import (
    InstanceResponseExecutionModeType2Type1,
    check_instance_response_execution_mode_type_2_type_1,
)
from ..models.instance_response_execution_mode_type_3_type_1 import (
    InstanceResponseExecutionModeType3Type1,
    check_instance_response_execution_mode_type_3_type_1,
)
from ..models.instance_response_lifecycle_failure_reason_type_1 import (
    InstanceResponseLifecycleFailureReasonType1,
    check_instance_response_lifecycle_failure_reason_type_1,
)
from ..models.instance_response_lifecycle_failure_reason_type_2_type_1 import (
    InstanceResponseLifecycleFailureReasonType2Type1,
    check_instance_response_lifecycle_failure_reason_type_2_type_1,
)
from ..models.instance_response_lifecycle_failure_reason_type_3_type_1 import (
    InstanceResponseLifecycleFailureReasonType3Type1,
    check_instance_response_lifecycle_failure_reason_type_3_type_1,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="InstanceResponse")


@_attrs_define
class InstanceResponse:
    """Read-only instance view: id, deployment, state (waking/running/...), lease uid, and host-side internal endpoint
    (loopback only).

    """

    id: str
    app_id: str
    deployment_id: str
    state: str
    ram_mb: int
    host_ip: None | str | Unset = UNSET
    wake_id: str | Unset = UNSET
    started_at: datetime.datetime | None | Unset = UNSET
    last_request_at: datetime.datetime | None | Unset = UNSET
    parked_at: datetime.datetime | None | Unset = UNSET
    min_instances_target: int | None | Unset = UNSET
    execution_mode: (
        InstanceResponseExecutionModeType1
        | InstanceResponseExecutionModeType2Type1
        | InstanceResponseExecutionModeType3Type1
        | None
        | Unset
    ) = UNSET
    """Closed-set execution mode for this instance (ADR-137 §Decision 1)."""
    lifecycle_failure_reason: (
        InstanceResponseLifecycleFailureReasonType1
        | InstanceResponseLifecycleFailureReasonType2Type1
        | InstanceResponseLifecycleFailureReasonType3Type1
        | None
        | Unset
    ) = UNSET
    """Reason for the most recent terminal transition (ADR-138 §Decision 2). null when still running."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        app_id = self.app_id

        deployment_id = self.deployment_id

        state = self.state

        ram_mb = self.ram_mb

        host_ip: None | str | Unset
        if isinstance(self.host_ip, Unset):
            host_ip = UNSET
        else:
            host_ip = self.host_ip

        wake_id = self.wake_id

        started_at: None | str | Unset
        if isinstance(self.started_at, Unset):
            started_at = UNSET
        elif isinstance(self.started_at, datetime.datetime):
            started_at = self.started_at.isoformat()
        else:
            started_at = self.started_at

        last_request_at: None | str | Unset
        if isinstance(self.last_request_at, Unset):
            last_request_at = UNSET
        elif isinstance(self.last_request_at, datetime.datetime):
            last_request_at = self.last_request_at.isoformat()
        else:
            last_request_at = self.last_request_at

        parked_at: None | str | Unset
        if isinstance(self.parked_at, Unset):
            parked_at = UNSET
        elif isinstance(self.parked_at, datetime.datetime):
            parked_at = self.parked_at.isoformat()
        else:
            parked_at = self.parked_at

        min_instances_target: int | None | Unset
        if isinstance(self.min_instances_target, Unset):
            min_instances_target = UNSET
        else:
            min_instances_target = self.min_instances_target

        execution_mode: None | str | Unset
        if isinstance(self.execution_mode, Unset):
            execution_mode = UNSET
        elif isinstance(self.execution_mode, str):
            execution_mode = self.execution_mode
        elif isinstance(self.execution_mode, str):
            execution_mode = self.execution_mode
        elif isinstance(self.execution_mode, str):
            execution_mode = self.execution_mode
        else:
            execution_mode = self.execution_mode

        lifecycle_failure_reason: None | str | Unset
        if isinstance(self.lifecycle_failure_reason, Unset):
            lifecycle_failure_reason = UNSET
        elif isinstance(self.lifecycle_failure_reason, str):
            lifecycle_failure_reason = self.lifecycle_failure_reason
        elif isinstance(self.lifecycle_failure_reason, str):
            lifecycle_failure_reason = self.lifecycle_failure_reason
        elif isinstance(self.lifecycle_failure_reason, str):
            lifecycle_failure_reason = self.lifecycle_failure_reason
        else:
            lifecycle_failure_reason = self.lifecycle_failure_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "state": state,
                "ram_mb": ram_mb,
            }
        )
        if host_ip is not UNSET:
            field_dict["host_ip"] = host_ip
        if wake_id is not UNSET:
            field_dict["wake_id"] = wake_id
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if last_request_at is not UNSET:
            field_dict["last_request_at"] = last_request_at
        if parked_at is not UNSET:
            field_dict["parked_at"] = parked_at
        if min_instances_target is not UNSET:
            field_dict["min_instances_target"] = min_instances_target
        if execution_mode is not UNSET:
            field_dict["execution_mode"] = execution_mode
        if lifecycle_failure_reason is not UNSET:
            field_dict["lifecycle_failure_reason"] = lifecycle_failure_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        app_id = d.pop("app_id")

        deployment_id = d.pop("deployment_id")

        state = d.pop("state")

        ram_mb = d.pop("ram_mb")

        def _parse_host_ip(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        host_ip = _parse_host_ip(d.pop("host_ip", UNSET))

        wake_id = d.pop("wake_id", UNSET)

        def _parse_started_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                started_at_type_0 = datetime.datetime.fromisoformat(data)

                return started_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        started_at = _parse_started_at(d.pop("started_at", UNSET))

        def _parse_last_request_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                last_request_at_type_0 = datetime.datetime.fromisoformat(data)

                return last_request_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        last_request_at = _parse_last_request_at(d.pop("last_request_at", UNSET))

        def _parse_parked_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                parked_at_type_0 = datetime.datetime.fromisoformat(data)

                return parked_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        parked_at = _parse_parked_at(d.pop("parked_at", UNSET))

        def _parse_min_instances_target(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        min_instances_target = _parse_min_instances_target(d.pop("min_instances_target", UNSET))

        def _parse_execution_mode(
            data: object,
        ) -> (
            InstanceResponseExecutionModeType1
            | InstanceResponseExecutionModeType2Type1
            | InstanceResponseExecutionModeType3Type1
            | None
            | Unset
        ):
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                execution_mode_type_1 = check_instance_response_execution_mode_type_1(data)

                return execution_mode_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                execution_mode_type_2_type_1 = check_instance_response_execution_mode_type_2_type_1(data)

                return execution_mode_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                execution_mode_type_3_type_1 = check_instance_response_execution_mode_type_3_type_1(data)

                return execution_mode_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                InstanceResponseExecutionModeType1
                | InstanceResponseExecutionModeType2Type1
                | InstanceResponseExecutionModeType3Type1
                | None
                | Unset,
                data,
            )

        execution_mode = _parse_execution_mode(d.pop("execution_mode", UNSET))

        def _parse_lifecycle_failure_reason(
            data: object,
        ) -> (
            InstanceResponseLifecycleFailureReasonType1
            | InstanceResponseLifecycleFailureReasonType2Type1
            | InstanceResponseLifecycleFailureReasonType3Type1
            | None
            | Unset
        ):
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                lifecycle_failure_reason_type_1 = check_instance_response_lifecycle_failure_reason_type_1(data)

                return lifecycle_failure_reason_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                lifecycle_failure_reason_type_2_type_1 = check_instance_response_lifecycle_failure_reason_type_2_type_1(
                    data
                )

                return lifecycle_failure_reason_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                lifecycle_failure_reason_type_3_type_1 = check_instance_response_lifecycle_failure_reason_type_3_type_1(
                    data
                )

                return lifecycle_failure_reason_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                InstanceResponseLifecycleFailureReasonType1
                | InstanceResponseLifecycleFailureReasonType2Type1
                | InstanceResponseLifecycleFailureReasonType3Type1
                | None
                | Unset,
                data,
            )

        lifecycle_failure_reason = _parse_lifecycle_failure_reason(d.pop("lifecycle_failure_reason", UNSET))

        instance_response = cls(
            id=id,
            app_id=app_id,
            deployment_id=deployment_id,
            state=state,
            ram_mb=ram_mb,
            host_ip=host_ip,
            wake_id=wake_id,
            started_at=started_at,
            last_request_at=last_request_at,
            parked_at=parked_at,
            min_instances_target=min_instances_target,
            execution_mode=execution_mode,
            lifecycle_failure_reason=lifecycle_failure_reason,
        )

        instance_response.additional_properties = d
        return instance_response

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
