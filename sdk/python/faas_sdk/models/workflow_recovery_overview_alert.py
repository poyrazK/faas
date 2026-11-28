from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_overview_alert_delivery_status import (
    WorkflowRecoveryOverviewAlertDeliveryStatus,
    check_workflow_recovery_overview_alert_delivery_status,
)

if TYPE_CHECKING:
    from ..models.workflow_recovery_polling_notification import WorkflowRecoveryPollingNotification


T = TypeVar("T", bound="WorkflowRecoveryOverviewAlert")


@_attrs_define
class WorkflowRecoveryOverviewAlert:
    """Delivery observations for frozen receipt receivers. Missing deliveries without an outbox entry are unknown. No
    receivers is not delivered.

    """

    notification: WorkflowRecoveryPollingNotification
    """Last captured polling alert receipt. queued means durable outbox admission; no_receivers never replays to
    later subscribers. Successful polling retains the recovered receipt separately from the cleared error metadata.
   """
    delivery_status: WorkflowRecoveryOverviewAlertDeliveryStatus
    expected: int
    pending: int
    in_flight: int
    succeeded: int
    failed: int
    dead: int
    missing: int
    relay_pending: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        notification = self.notification.to_dict()

        delivery_status: str = self.delivery_status

        expected = self.expected

        pending = self.pending

        in_flight = self.in_flight

        succeeded = self.succeeded

        failed = self.failed

        dead = self.dead

        missing = self.missing

        relay_pending = self.relay_pending

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "notification": notification,
                "delivery_status": delivery_status,
                "expected": expected,
                "pending": pending,
                "in_flight": in_flight,
                "succeeded": succeeded,
                "failed": failed,
                "dead": dead,
                "missing": missing,
                "relay_pending": relay_pending,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_polling_notification import WorkflowRecoveryPollingNotification

        d = dict(src_dict)
        notification = WorkflowRecoveryPollingNotification.from_dict(d.pop("notification"))

        delivery_status = check_workflow_recovery_overview_alert_delivery_status(d.pop("delivery_status"))

        expected = d.pop("expected")

        pending = d.pop("pending")

        in_flight = d.pop("in_flight")

        succeeded = d.pop("succeeded")

        failed = d.pop("failed")

        dead = d.pop("dead")

        missing = d.pop("missing")

        relay_pending = d.pop("relay_pending")

        workflow_recovery_overview_alert = cls(
            notification=notification,
            delivery_status=delivery_status,
            expected=expected,
            pending=pending,
            in_flight=in_flight,
            succeeded=succeeded,
            failed=failed,
            dead=dead,
            missing=missing,
            relay_pending=relay_pending,
        )

        workflow_recovery_overview_alert.additional_properties = d
        return workflow_recovery_overview_alert

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
