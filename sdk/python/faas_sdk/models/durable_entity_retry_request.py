from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.durable_entity_retry_request_target import (
    DurableEntityRetryRequestTarget,
    check_durable_entity_retry_request_target,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DurableEntityRetryRequest")


@_attrs_define
class DurableEntityRetryRequest:
    """For target alarm provide alarm_at and omit head_id; for outbox provide
    head_id and omit alarm_at. Copy comparison fields from fresh inspection.

    """

    namespace: str
    key: str
    target: DurableEntityRetryRequestTarget
    expected_version: int
    """Exact business uint64 version from inspection."""
    expected_recovery_revision: str
    """Opaque revision from the same inspection, invalidated by every manifest write."""
    environment: str | Unset = UNSET
    platform_tenant_id: UUID | Unset = UNSET
    head_id: UUID | Unset = UNSET
    alarm_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        namespace = self.namespace

        key = self.key

        target: str = self.target

        expected_version = self.expected_version

        expected_recovery_revision = self.expected_recovery_revision

        environment = self.environment

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        head_id: str | Unset = UNSET
        if not isinstance(self.head_id, Unset):
            head_id = str(self.head_id)

        alarm_at: str | Unset = UNSET
        if not isinstance(self.alarm_at, Unset):
            alarm_at = self.alarm_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "namespace": namespace,
                "key": key,
                "target": target,
                "expected_version": expected_version,
                "expected_recovery_revision": expected_recovery_revision,
            }
        )
        if environment is not UNSET:
            field_dict["environment"] = environment
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if head_id is not UNSET:
            field_dict["head_id"] = head_id
        if alarm_at is not UNSET:
            field_dict["alarm_at"] = alarm_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        namespace = d.pop("namespace")

        key = d.pop("key")

        target = check_durable_entity_retry_request_target(d.pop("target"))

        expected_version = d.pop("expected_version")

        expected_recovery_revision = d.pop("expected_recovery_revision")

        environment = d.pop("environment", UNSET)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        _head_id = d.pop("head_id", UNSET)
        head_id: UUID | Unset
        if isinstance(_head_id, Unset):
            head_id = UNSET
        else:
            head_id = UUID(_head_id)

        _alarm_at = d.pop("alarm_at", UNSET)
        alarm_at: datetime.datetime | Unset
        if isinstance(_alarm_at, Unset):
            alarm_at = UNSET
        else:
            alarm_at = datetime.datetime.fromisoformat(_alarm_at)

        durable_entity_retry_request = cls(
            namespace=namespace,
            key=key,
            target=target,
            expected_version=expected_version,
            expected_recovery_revision=expected_recovery_revision,
            environment=environment,
            platform_tenant_id=platform_tenant_id,
            head_id=head_id,
            alarm_at=alarm_at,
        )

        return durable_entity_retry_request
