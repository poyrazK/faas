from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.put_operation_webhook_binding_request_mode import (
    PutOperationWebhookBindingRequestMode,
    check_put_operation_webhook_binding_request_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject_spec import OperationSubjectSpec
    from ..models.operation_webhook_signal import OperationWebhookSignal


T = TypeVar("T", bound="PutOperationWebhookBindingRequest")


@_attrs_define
class PutOperationWebhookBindingRequest:
    """Bind a verified provider endpoint to a customer operation with revision control."""

    expected_version: int
    operation: str
    """Operation name resolved on the scope's live deployment for each event."""
    platform_tenant_id: UUID
    """Operation owner fixed by this binding."""
    take_over_delivery: bool
    scope: str | Unset = UNSET
    """Environment scope; defaults to default."""
    event_types: list[str] | Unset = UNSET
    """Exact event types to admit; empty admits every verified event. Must be empty in signal mode."""
    mode: PutOperationWebhookBindingRequestMode | Unset = "start"
    """start admits a new operation per event; signal resumes waiting operations."""
    subject: OperationSubjectSpec | Unset = UNSET
    """Optional public business reference extracted once from validated input on new admission. This metadata does
    not authorize access to the business entity."""
    signals: list[OperationWebhookSignal] | Unset = UNSET
    """Signal mode only. Provider event types to deliver and the operation event each becomes; other types are
    acknowledged as ignored."""

    def to_dict(self) -> dict[str, Any]:
        expected_version = self.expected_version

        operation = self.operation

        platform_tenant_id = str(self.platform_tenant_id)

        take_over_delivery = self.take_over_delivery

        scope = self.scope

        event_types: list[str] | Unset = UNSET
        if not isinstance(self.event_types, Unset):
            event_types = self.event_types

        mode: str | Unset = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode

        subject: dict[str, Any] | Unset = UNSET
        if not isinstance(self.subject, Unset):
            subject = self.subject.to_dict()

        signals: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.signals, Unset):
            signals = []
            for signals_item_data in self.signals:
                signals_item = signals_item_data.to_dict()
                signals.append(signals_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_version": expected_version,
                "operation": operation,
                "platform_tenant_id": platform_tenant_id,
                "take_over_delivery": take_over_delivery,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope
        if event_types is not UNSET:
            field_dict["event_types"] = event_types
        if mode is not UNSET:
            field_dict["mode"] = mode
        if subject is not UNSET:
            field_dict["subject"] = subject
        if signals is not UNSET:
            field_dict["signals"] = signals

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject_spec import OperationSubjectSpec
        from ..models.operation_webhook_signal import OperationWebhookSignal

        d = dict(src_dict)
        expected_version = d.pop("expected_version")

        operation = d.pop("operation")

        platform_tenant_id = UUID(d.pop("platform_tenant_id"))

        take_over_delivery = d.pop("take_over_delivery")

        scope = d.pop("scope", UNSET)

        event_types = cast(list[str], d.pop("event_types", UNSET))

        _mode = d.pop("mode", UNSET)
        mode: PutOperationWebhookBindingRequestMode | Unset
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = check_put_operation_webhook_binding_request_mode(_mode)

        _subject = d.pop("subject", UNSET)
        subject: OperationSubjectSpec | Unset
        if isinstance(_subject, Unset):
            subject = UNSET
        else:
            subject = OperationSubjectSpec.from_dict(_subject)

        _signals = d.pop("signals", UNSET)
        signals: list[OperationWebhookSignal] | Unset = UNSET
        if _signals is not UNSET:
            signals = []
            for signals_item_data in _signals:
                signals_item = OperationWebhookSignal.from_dict(signals_item_data)

                signals.append(signals_item)

        put_operation_webhook_binding_request = cls(
            expected_version=expected_version,
            operation=operation,
            platform_tenant_id=platform_tenant_id,
            take_over_delivery=take_over_delivery,
            scope=scope,
            event_types=event_types,
            mode=mode,
            subject=subject,
            signals=signals,
        )

        return put_operation_webhook_binding_request
