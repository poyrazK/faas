from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.event_recovery_notification_retry_result import EventRecoveryNotificationRetryResult


T = TypeVar("T", bound="EventRecoveryNotificationRetryResponse")


@_attrs_define
class EventRecoveryNotificationRetryResponse:
    """Durable metadata-only receiver decisions, sorted by delivery ID and retained with the owning recovery job. Retrying
    identical intent returns this original response, including skipped decisions and decision time.

    """

    job_id: UUID
    app_id: UUID
    request_id: UUID
    decided_at: datetime.datetime
    results: list[EventRecoveryNotificationRetryResult]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        app_id = str(self.app_id)

        request_id = str(self.request_id)

        decided_at = self.decided_at.isoformat()

        results = []
        for results_item_data in self.results:
            results_item = results_item_data.to_dict()
            results.append(results_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job_id": job_id,
                "app_id": app_id,
                "request_id": request_id,
                "decided_at": decided_at,
                "results": results,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_notification_retry_result import EventRecoveryNotificationRetryResult

        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        app_id = UUID(d.pop("app_id"))

        request_id = UUID(d.pop("request_id"))

        decided_at = datetime.datetime.fromisoformat(d.pop("decided_at"))

        results = []
        _results = d.pop("results")
        for results_item_data in _results:
            results_item = EventRecoveryNotificationRetryResult.from_dict(results_item_data)

            results.append(results_item)

        event_recovery_notification_retry_response = cls(
            job_id=job_id,
            app_id=app_id,
            request_id=request_id,
            decided_at=decided_at,
            results=results,
        )

        event_recovery_notification_retry_response.additional_properties = d
        return event_recovery_notification_retry_response

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
