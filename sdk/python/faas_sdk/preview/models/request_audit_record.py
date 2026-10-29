from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RequestAuditRecord")


@_attrs_define
class RequestAuditRecord:
    """One non-collapsed completed request observed at the gateway."""

    event_id: UUID
    account_id: UUID
    app_id: UUID
    route_template: str
    method: str
    http_status: int
    latency_ms: int
    occurred_at: datetime.datetime
    consumer_id: UUID | Unset = UNSET
    platform_tenant_id: UUID | Unset = UNSET
    trace_id: str | Unset = UNSET
    deployment_id: UUID | Unset = UNSET
    commit_sha: str | Unset = UNSET
    request_id: str | Unset = UNSET
    source_ip: str | Unset = UNSET
    """Verified public-gateway client IP when available."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        account_id = str(self.account_id)

        app_id = str(self.app_id)

        route_template = self.route_template

        method = self.method

        http_status = self.http_status

        latency_ms = self.latency_ms

        occurred_at = self.occurred_at.isoformat()

        consumer_id: str | Unset = UNSET
        if not isinstance(self.consumer_id, Unset):
            consumer_id = str(self.consumer_id)

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        trace_id = self.trace_id

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        commit_sha = self.commit_sha

        request_id = self.request_id

        source_ip = self.source_ip

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "account_id": account_id,
                "app_id": app_id,
                "route_template": route_template,
                "method": method,
                "http_status": http_status,
                "latency_ms": latency_ms,
                "occurred_at": occurred_at,
            }
        )
        if consumer_id is not UNSET:
            field_dict["consumer_id"] = consumer_id
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if commit_sha is not UNSET:
            field_dict["commit_sha"] = commit_sha
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if source_ip is not UNSET:
            field_dict["source_ip"] = source_ip

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        account_id = UUID(d.pop("account_id"))

        app_id = UUID(d.pop("app_id"))

        route_template = d.pop("route_template")

        method = d.pop("method")

        http_status = d.pop("http_status")

        latency_ms = d.pop("latency_ms")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        _consumer_id = d.pop("consumer_id", UNSET)
        consumer_id: UUID | Unset
        if isinstance(_consumer_id, Unset):
            consumer_id = UNSET
        else:
            consumer_id = UUID(_consumer_id)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        trace_id = d.pop("trace_id", UNSET)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        commit_sha = d.pop("commit_sha", UNSET)

        request_id = d.pop("request_id", UNSET)

        source_ip = d.pop("source_ip", UNSET)

        request_audit_record = cls(
            event_id=event_id,
            account_id=account_id,
            app_id=app_id,
            route_template=route_template,
            method=method,
            http_status=http_status,
            latency_ms=latency_ms,
            occurred_at=occurred_at,
            consumer_id=consumer_id,
            platform_tenant_id=platform_tenant_id,
            trace_id=trace_id,
            deployment_id=deployment_id,
            commit_sha=commit_sha,
            request_id=request_id,
            source_ip=source_ip,
        )

        request_audit_record.additional_properties = d
        return request_audit_record

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
