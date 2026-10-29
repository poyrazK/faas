from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkerAdmissionBlocker")


@_attrs_define
class WorkerAdmissionBlocker:
    """Scheduler-owned admission evidence. Codes currently include account_worker_limit, concurrency_limit, node_ram,
    node_vcpu, node_cpu, no_admitting_nodes, node_ports, placement_changed, spend_cap, retry_cooldown, admission_refused
    and deployment_unavailable. Node constraints omit host identities and fleet usage. Values for account and
    concurrency budgets describe the actual decision, including residents still draining.

    """

    code: str
    observed_at: datetime.datetime
    stale: bool | Unset = UNSET
    """Whether this refusal observation is older than fifteen minutes, independently of the pool counts."""
    resource: str | Unset = UNSET
    """Units for numeric evidence, such as replicas, ram_mb, vcpu, cpu_millicores or cents."""
    requested: int | Unset = UNSET
    used: int | Unset = UNSET
    limit: int | Unset = UNSET
    retry_at: datetime.datetime | Unset = UNSET
    """App-wide admission cooldown deadline when code is retry_cooldown."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        observed_at = self.observed_at.isoformat()

        stale = self.stale

        resource = self.resource

        requested = self.requested

        used = self.used

        limit = self.limit

        retry_at: str | Unset = UNSET
        if not isinstance(self.retry_at, Unset):
            retry_at = self.retry_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "observed_at": observed_at,
            }
        )
        if stale is not UNSET:
            field_dict["stale"] = stale
        if resource is not UNSET:
            field_dict["resource"] = resource
        if requested is not UNSET:
            field_dict["requested"] = requested
        if used is not UNSET:
            field_dict["used"] = used
        if limit is not UNSET:
            field_dict["limit"] = limit
        if retry_at is not UNSET:
            field_dict["retry_at"] = retry_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        stale = d.pop("stale", UNSET)

        resource = d.pop("resource", UNSET)

        requested = d.pop("requested", UNSET)

        used = d.pop("used", UNSET)

        limit = d.pop("limit", UNSET)

        _retry_at = d.pop("retry_at", UNSET)
        retry_at: datetime.datetime | Unset
        if isinstance(_retry_at, Unset):
            retry_at = UNSET
        else:
            retry_at = datetime.datetime.fromisoformat(_retry_at)

        worker_admission_blocker = cls(
            code=code,
            observed_at=observed_at,
            stale=stale,
            resource=resource,
            requested=requested,
            used=used,
            limit=limit,
            retry_at=retry_at,
        )

        worker_admission_blocker.additional_properties = d
        return worker_admission_blocker

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
