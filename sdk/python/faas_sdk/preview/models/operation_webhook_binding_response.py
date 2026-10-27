from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_webhook_binding_response_mode import (
    OperationWebhookBindingResponseMode,
    check_operation_webhook_binding_response_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject_spec import OperationSubjectSpec
    from ..models.operation_webhook_signal import OperationWebhookSignal


T = TypeVar("T", bound="OperationWebhookBindingResponse")


@_attrs_define
class OperationWebhookBindingResponse:
    """Bound operation, owner, mode, event selection and current endpoint binding revision."""

    endpoint_id: UUID
    app_id: UUID
    scope: str
    operation: str
    platform_tenant_id: UUID
    event_types: list[str]
    mode: OperationWebhookBindingResponseMode
    version: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    subject: OperationSubjectSpec | Unset = UNSET
    """Optional public business reference extracted once from validated input on new admission. This metadata does
    not authorize access to the business entity."""
    signals: list[OperationWebhookSignal] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        endpoint_id = str(self.endpoint_id)

        app_id = str(self.app_id)

        scope = self.scope

        operation = self.operation

        platform_tenant_id = str(self.platform_tenant_id)

        event_types = self.event_types

        mode: str = self.mode

        version = self.version

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

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
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "endpoint_id": endpoint_id,
                "app_id": app_id,
                "scope": scope,
                "operation": operation,
                "platform_tenant_id": platform_tenant_id,
                "event_types": event_types,
                "mode": mode,
                "version": version,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
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
        endpoint_id = UUID(d.pop("endpoint_id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        operation = d.pop("operation")

        platform_tenant_id = UUID(d.pop("platform_tenant_id"))

        event_types = cast(list[str], d.pop("event_types"))

        mode = check_operation_webhook_binding_response_mode(d.pop("mode"))

        version = d.pop("version")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

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

        operation_webhook_binding_response = cls(
            endpoint_id=endpoint_id,
            app_id=app_id,
            scope=scope,
            operation=operation,
            platform_tenant_id=platform_tenant_id,
            event_types=event_types,
            mode=mode,
            version=version,
            created_at=created_at,
            updated_at=updated_at,
            subject=subject,
            signals=signals,
        )

        operation_webhook_binding_response.additional_properties = d
        return operation_webhook_binding_response

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
