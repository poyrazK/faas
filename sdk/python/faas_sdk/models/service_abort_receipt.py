from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.service_abort_receipt_scope import ServiceAbortReceiptScope, check_service_abort_receipt_scope

T = TypeVar("T", bound="ServiceAbortReceipt")


@_attrs_define
class ServiceAbortReceipt:
    """Immutable acceptance of an original reviewed service-abort intent; ACK/drain completion and the current handoff
    remain separate.

    """

    attempt_id: str
    request_hash: str
    app_id: UUID
    deployment_id: UUID
    predecessor_deployment_id: UUID
    request_id: UUID
    scope: ServiceAbortReceiptScope
    accepted_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempt_id = self.attempt_id

        request_hash = self.request_hash

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        predecessor_deployment_id = str(self.predecessor_deployment_id)

        request_id = str(self.request_id)

        scope: str = self.scope

        accepted_at = self.accepted_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempt_id": attempt_id,
                "request_hash": request_hash,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "predecessor_deployment_id": predecessor_deployment_id,
                "request_id": request_id,
                "scope": scope,
                "accepted_at": accepted_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attempt_id = d.pop("attempt_id")

        request_hash = d.pop("request_hash")

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        predecessor_deployment_id = UUID(d.pop("predecessor_deployment_id"))

        request_id = UUID(d.pop("request_id"))

        scope = check_service_abort_receipt_scope(d.pop("scope"))

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        service_abort_receipt = cls(
            attempt_id=attempt_id,
            request_hash=request_hash,
            app_id=app_id,
            deployment_id=deployment_id,
            predecessor_deployment_id=predecessor_deployment_id,
            request_id=request_id,
            scope=scope,
            accepted_at=accepted_at,
        )

        service_abort_receipt.additional_properties = d
        return service_abort_receipt

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
