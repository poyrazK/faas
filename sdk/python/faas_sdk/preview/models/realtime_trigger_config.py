from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeTriggerConfig")


@_attrs_define
class RealtimeTriggerConfig:
    """Config of a `kind: realtime` trigger. Each message of the bound consumer group is delivered as one
    invocation. The group must already exist in the caller's account with `lease_seconds` of at least 120, and binds
    to at most one trigger; while bound, direct claims and settlements return 409 `realtime_group_bound_to_trigger`.
    The group's ordering, filters, retry policy and dead-letter queue govern delivery. The binding is immutable;
    only `max_concurrency` can change.

    """

    endpoint_id: UUID
    channel: str
    group: str
    max_concurrency: int | Unset = UNSET
    """Parallel invocations; 0 means 1. Also capped by the plan's concurrency."""

    def to_dict(self) -> dict[str, Any]:
        endpoint_id = str(self.endpoint_id)

        channel = self.channel

        group = self.group

        max_concurrency = self.max_concurrency

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "endpoint_id": endpoint_id,
                "channel": channel,
                "group": group,
            }
        )
        if max_concurrency is not UNSET:
            field_dict["max_concurrency"] = max_concurrency

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        endpoint_id = UUID(d.pop("endpoint_id"))

        channel = d.pop("channel")

        group = d.pop("group")

        max_concurrency = d.pop("max_concurrency", UNSET)

        realtime_trigger_config = cls(
            endpoint_id=endpoint_id,
            channel=channel,
            group=group,
            max_concurrency=max_concurrency,
        )

        return realtime_trigger_config
