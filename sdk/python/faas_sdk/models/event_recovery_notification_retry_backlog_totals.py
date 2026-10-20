from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EventRecoveryNotificationRetryBacklogTotals")


@_attrs_define
class EventRecoveryNotificationRetryBacklogTotals:
    """Counts of every saved retry request in the inspected app job page before filtering. Request statuses partition
    request_count and incomplete evidence overlaps those statuses.

    """

    request_count: int
    succeeded_count: int
    failed_count: int
    pending_count: int
    inconclusive_count: int
    incomplete_evidence_count: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_count = self.request_count

        succeeded_count = self.succeeded_count

        failed_count = self.failed_count

        pending_count = self.pending_count

        inconclusive_count = self.inconclusive_count

        incomplete_evidence_count = self.incomplete_evidence_count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_count": request_count,
                "succeeded_count": succeeded_count,
                "failed_count": failed_count,
                "pending_count": pending_count,
                "inconclusive_count": inconclusive_count,
                "incomplete_evidence_count": incomplete_evidence_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        request_count = d.pop("request_count")

        succeeded_count = d.pop("succeeded_count")

        failed_count = d.pop("failed_count")

        pending_count = d.pop("pending_count")

        inconclusive_count = d.pop("inconclusive_count")

        incomplete_evidence_count = d.pop("incomplete_evidence_count")

        event_recovery_notification_retry_backlog_totals = cls(
            request_count=request_count,
            succeeded_count=succeeded_count,
            failed_count=failed_count,
            pending_count=pending_count,
            inconclusive_count=inconclusive_count,
            incomplete_evidence_count=incomplete_evidence_count,
        )

        event_recovery_notification_retry_backlog_totals.additional_properties = d
        return event_recovery_notification_retry_backlog_totals

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
