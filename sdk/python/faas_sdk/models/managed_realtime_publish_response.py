from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimePublishResponse")


@_attrs_define
class ManagedRealtimePublishResponse:
    """Per-recipient queue outcome for a channel publish; queue admission does not imply client receipt."""

    queued: int
    """Number of live output queues or retained-resume wake-ups that accepted delivery work."""
    subscribers: int | Unset = UNSET
    """Live and resumable subscribers targeted across reachable nodes."""
    queue_full: int | Unset = UNSET
    """Subscribers whose bounded output queues were full."""
    failed: int | Unset = UNSET
    """Target subscribers that could not be queued for another per-connection reason."""
    partial: bool | Unset = UNSET
    """Whether a node or any target subscriber did not accept the publish."""
    nodes_queried: int | Unset = UNSET
    """Active realtime nodes that accepted the publish request."""
    nodes_unavailable: int | Unset = UNSET
    """Active realtime nodes that did not accept the publish request."""
    sequence: int | Unset = UNSET
    """Committed channel sequence; present when durable is true."""
    durable: bool | Unset = UNSET
    """Whether this publish was committed to retained channel history before fan-out."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        queued = self.queued

        subscribers = self.subscribers

        queue_full = self.queue_full

        failed = self.failed

        partial = self.partial

        nodes_queried = self.nodes_queried

        nodes_unavailable = self.nodes_unavailable

        sequence = self.sequence

        durable = self.durable

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "queued": queued,
            }
        )
        if subscribers is not UNSET:
            field_dict["subscribers"] = subscribers
        if queue_full is not UNSET:
            field_dict["queue_full"] = queue_full
        if failed is not UNSET:
            field_dict["failed"] = failed
        if partial is not UNSET:
            field_dict["partial"] = partial
        if nodes_queried is not UNSET:
            field_dict["nodes_queried"] = nodes_queried
        if nodes_unavailable is not UNSET:
            field_dict["nodes_unavailable"] = nodes_unavailable
        if sequence is not UNSET:
            field_dict["sequence"] = sequence
        if durable is not UNSET:
            field_dict["durable"] = durable

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        queued = d.pop("queued")

        subscribers = d.pop("subscribers", UNSET)

        queue_full = d.pop("queue_full", UNSET)

        failed = d.pop("failed", UNSET)

        partial = d.pop("partial", UNSET)

        nodes_queried = d.pop("nodes_queried", UNSET)

        nodes_unavailable = d.pop("nodes_unavailable", UNSET)

        sequence = d.pop("sequence", UNSET)

        durable = d.pop("durable", UNSET)

        managed_realtime_publish_response = cls(
            queued=queued,
            subscribers=subscribers,
            queue_full=queue_full,
            failed=failed,
            partial=partial,
            nodes_queried=nodes_queried,
            nodes_unavailable=nodes_unavailable,
            sequence=sequence,
            durable=durable,
        )

        managed_realtime_publish_response.additional_properties = d
        return managed_realtime_publish_response

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
