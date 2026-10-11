from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, Literal, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_backlog_consumer import EventBacklogConsumer
    from ..models.event_backlog_recipient import EventBacklogRecipient


T = TypeVar("T", bound="EventBacklogResponse")


@_attrs_define
class EventBacklogResponse:
    """Live waiting recipients and exact matching counts, with an anchored acceptance window.

    Example:
        {'observed_at': '2026-10-05T12:00:00Z', 'window_at': '2026-10-05T12:00:00Z', 'coverage':
            'captured_and_backfill_recipients', 'recipients': [], 'consumers': [], 'unattributed_receipts': 0}

    """

    observed_at: datetime.datetime
    """Time of this live observation."""
    window_at: datetime.datetime
    """First-page time anchoring acceptance and minimum-age cutoff across pages."""
    coverage: Literal["captured_and_backfill_recipients"]
    recipients: list[EventBacklogRecipient]
    consumers: list[EventBacklogConsumer]
    unattributed_receipts: int
    """Account-wide unresolved receipts without a captured snapshot; only the age/acceptance window applies."""
    next_after: str | Unset = UNSET
    """Opaque continuation for the recipient page."""
    next_consumers_after: str | Unset = UNSET
    """Opaque continuation for the consumer page."""

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        window_at = self.window_at.isoformat()

        coverage = self.coverage

        recipients = []
        for recipients_item_data in self.recipients:
            recipients_item = recipients_item_data.to_dict()
            recipients.append(recipients_item)

        consumers = []
        for consumers_item_data in self.consumers:
            consumers_item = consumers_item_data.to_dict()
            consumers.append(consumers_item)

        unattributed_receipts = self.unattributed_receipts

        next_after = self.next_after

        next_consumers_after = self.next_consumers_after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "observed_at": observed_at,
                "window_at": window_at,
                "coverage": coverage,
                "recipients": recipients,
                "consumers": consumers,
                "unattributed_receipts": unattributed_receipts,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after
        if next_consumers_after is not UNSET:
            field_dict["next_consumers_after"] = next_consumers_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_backlog_consumer import EventBacklogConsumer
        from ..models.event_backlog_recipient import EventBacklogRecipient

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        window_at = datetime.datetime.fromisoformat(d.pop("window_at"))

        coverage = cast(Literal["captured_and_backfill_recipients"], d.pop("coverage"))
        if coverage != "captured_and_backfill_recipients":
            raise ValueError(f"coverage must match const 'captured_and_backfill_recipients', got '{coverage}'")

        recipients = []
        _recipients = d.pop("recipients")
        for recipients_item_data in _recipients:
            recipients_item = EventBacklogRecipient.from_dict(recipients_item_data)

            recipients.append(recipients_item)

        consumers = []
        _consumers = d.pop("consumers")
        for consumers_item_data in _consumers:
            consumers_item = EventBacklogConsumer.from_dict(consumers_item_data)

            consumers.append(consumers_item)

        unattributed_receipts = d.pop("unattributed_receipts")

        next_after = d.pop("next_after", UNSET)

        next_consumers_after = d.pop("next_consumers_after", UNSET)

        event_backlog_response = cls(
            observed_at=observed_at,
            window_at=window_at,
            coverage=coverage,
            recipients=recipients,
            consumers=consumers,
            unattributed_receipts=unattributed_receipts,
            next_after=next_after,
            next_consumers_after=next_consumers_after,
        )

        return event_backlog_response
