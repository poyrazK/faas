from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.realtime_channel_webhook_filter_metadata import RealtimeChannelWebhookFilterMetadata


T = TypeVar("T", bound="RealtimeChannelWebhookFilter")


@_attrs_define
class RealtimeChannelWebhookFilter:
    """Realtime Channel Webhook Filter."""

    endpoint_id: str
    channel: str
    metadata: RealtimeChannelWebhookFilterMetadata | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        endpoint_id = self.endpoint_id

        channel = self.channel

        metadata: dict[str, Any] | Unset = UNSET
        if not isinstance(self.metadata, Unset):
            metadata = self.metadata.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "endpoint_id": endpoint_id,
                "channel": channel,
            }
        )
        if metadata is not UNSET:
            field_dict["metadata"] = metadata

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_channel_webhook_filter_metadata import RealtimeChannelWebhookFilterMetadata

        d = dict(src_dict)
        endpoint_id = d.pop("endpoint_id")

        channel = d.pop("channel")

        _metadata = d.pop("metadata", UNSET)
        metadata: RealtimeChannelWebhookFilterMetadata | Unset
        if isinstance(_metadata, Unset):
            metadata = UNSET
        else:
            metadata = RealtimeChannelWebhookFilterMetadata.from_dict(_metadata)

        realtime_channel_webhook_filter = cls(
            endpoint_id=endpoint_id,
            channel=channel,
            metadata=metadata,
        )

        return realtime_channel_webhook_filter
