from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_staging_receipt_status import (
    DeploymentStagingReceiptStatus,
    check_deployment_staging_receipt_status,
)

T = TypeVar("T", bound="DeploymentStagingReceipt")


@_attrs_define
class DeploymentStagingReceipt:
    """The outcome of an atomic deployment policy staging or cleanup operation."""

    session_id: str
    app_id: UUID
    status: DeploymentStagingReceiptStatus
    revision: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        session_id = self.session_id

        app_id = str(self.app_id)

        status: str = self.status

        revision = self.revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "session_id": session_id,
                "app_id": app_id,
                "status": status,
                "revision": revision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        session_id = d.pop("session_id")

        app_id = UUID(d.pop("app_id"))

        status = check_deployment_staging_receipt_status(d.pop("status"))

        revision = d.pop("revision")

        deployment_staging_receipt = cls(
            session_id=session_id,
            app_id=app_id,
            status=status,
            revision=revision,
        )

        deployment_staging_receipt.additional_properties = d
        return deployment_staging_receipt

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
