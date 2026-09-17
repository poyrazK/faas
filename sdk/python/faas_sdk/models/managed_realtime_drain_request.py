from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimeDrainRequest")


@_attrs_define
class ManagedRealtimeDrainRequest:
    """Bounded and auditable selection for closing live connections."""

    reason: str
    """Required reason recorded in the audit event and close request."""
    channel: str | Unset = UNSET
    """Only select connections subscribed to this channel."""
    principal: str | Unset = UNSET
    """Only select connections for this authenticated principal."""
    connection_ids: list[str] | Unset = UNSET
    """Explicit connection IDs to select in addition to the filters."""
    limit: int | Unset = 100
    """Maximum number of connections to select."""
    dry_run: bool | Unset = False
    """Return the selection without closing any connection."""
    allow_partial: bool | Unset = False
    """Permit closing the reachable subset when some nodes are unavailable."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reason = self.reason

        channel = self.channel

        principal = self.principal

        connection_ids: list[str] | Unset = UNSET
        if not isinstance(self.connection_ids, Unset):
            connection_ids = self.connection_ids

        limit = self.limit

        dry_run = self.dry_run

        allow_partial = self.allow_partial

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reason": reason,
            }
        )
        if channel is not UNSET:
            field_dict["channel"] = channel
        if principal is not UNSET:
            field_dict["principal"] = principal
        if connection_ids is not UNSET:
            field_dict["connection_ids"] = connection_ids
        if limit is not UNSET:
            field_dict["limit"] = limit
        if dry_run is not UNSET:
            field_dict["dry_run"] = dry_run
        if allow_partial is not UNSET:
            field_dict["allow_partial"] = allow_partial

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reason = d.pop("reason")

        channel = d.pop("channel", UNSET)

        principal = d.pop("principal", UNSET)

        connection_ids = cast(list[str], d.pop("connection_ids", UNSET))

        limit = d.pop("limit", UNSET)

        dry_run = d.pop("dry_run", UNSET)

        allow_partial = d.pop("allow_partial", UNSET)

        managed_realtime_drain_request = cls(
            reason=reason,
            channel=channel,
            principal=principal,
            connection_ids=connection_ids,
            limit=limit,
            dry_run=dry_run,
            allow_partial=allow_partial,
        )

        managed_realtime_drain_request.additional_properties = d
        return managed_realtime_drain_request

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
