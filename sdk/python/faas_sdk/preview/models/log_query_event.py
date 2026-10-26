from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.log_query_event_level import LogQueryEventLevel, check_log_query_event_level
from ..models.log_query_event_source import LogQueryEventSource, check_log_query_event_source
from ..models.log_query_event_stream import LogQueryEventStream, check_log_query_event_stream
from ..types import UNSET, Unset

T = TypeVar("T", bound="LogQueryEvent")


@_attrs_define
class LogQueryEvent:
    """Safe source-neutral log query event; trace lookup returns the HTTP access-log projection."""

    id: UUID
    timestamp: datetime.datetime
    source: LogQueryEventSource
    message: str
    app: str | Unset = UNSET
    deployment_id: UUID | Unset = UNSET
    instance_id: str | Unset = UNSET
    request_id: str | Unset = UNSET
    trace_id: str | Unset = UNSET
    route: str | Unset = UNSET
    method: str | Unset = UNSET
    status: int | Unset = UNSET
    level: LogQueryEventLevel | Unset = UNSET
    stream: LogQueryEventStream | Unset = UNSET
    latency_ms: int | Unset = UNSET
    count: int | Unset = UNSET
    cold_boot: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        timestamp = self.timestamp.isoformat()

        source: str = self.source

        message = self.message

        app = self.app

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        instance_id = self.instance_id

        request_id = self.request_id

        trace_id = self.trace_id

        route = self.route

        method = self.method

        status = self.status

        level: str | Unset = UNSET
        if not isinstance(self.level, Unset):
            level = self.level

        stream: str | Unset = UNSET
        if not isinstance(self.stream, Unset):
            stream = self.stream

        latency_ms = self.latency_ms

        count = self.count

        cold_boot = self.cold_boot

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "timestamp": timestamp,
                "source": source,
                "message": message,
            }
        )
        if app is not UNSET:
            field_dict["app"] = app
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if route is not UNSET:
            field_dict["route"] = route
        if method is not UNSET:
            field_dict["method"] = method
        if status is not UNSET:
            field_dict["status"] = status
        if level is not UNSET:
            field_dict["level"] = level
        if stream is not UNSET:
            field_dict["stream"] = stream
        if latency_ms is not UNSET:
            field_dict["latency_ms"] = latency_ms
        if count is not UNSET:
            field_dict["count"] = count
        if cold_boot is not UNSET:
            field_dict["cold_boot"] = cold_boot

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        timestamp = datetime.datetime.fromisoformat(d.pop("timestamp"))

        source = check_log_query_event_source(d.pop("source"))

        message = d.pop("message")

        app = d.pop("app", UNSET)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        instance_id = d.pop("instance_id", UNSET)

        request_id = d.pop("request_id", UNSET)

        trace_id = d.pop("trace_id", UNSET)

        route = d.pop("route", UNSET)

        method = d.pop("method", UNSET)

        status = d.pop("status", UNSET)

        _level = d.pop("level", UNSET)
        level: LogQueryEventLevel | Unset
        if isinstance(_level, Unset):
            level = UNSET
        else:
            level = check_log_query_event_level(_level)

        _stream = d.pop("stream", UNSET)
        stream: LogQueryEventStream | Unset
        if isinstance(_stream, Unset):
            stream = UNSET
        else:
            stream = check_log_query_event_stream(_stream)

        latency_ms = d.pop("latency_ms", UNSET)

        count = d.pop("count", UNSET)

        cold_boot = d.pop("cold_boot", UNSET)

        log_query_event = cls(
            id=id,
            timestamp=timestamp,
            source=source,
            message=message,
            app=app,
            deployment_id=deployment_id,
            instance_id=instance_id,
            request_id=request_id,
            trace_id=trace_id,
            route=route,
            method=method,
            status=status,
            level=level,
            stream=stream,
            latency_ms=latency_ms,
            count=count,
            cold_boot=cold_boot,
        )

        log_query_event.additional_properties = d
        return log_query_event

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
