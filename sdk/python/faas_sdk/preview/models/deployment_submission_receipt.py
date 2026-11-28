from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DeploymentSubmissionReceipt")


@_attrs_define
class DeploymentSubmissionReceipt:
    """Immutable acceptance evidence for one opaque deployment submission attempt."""

    attempt_id: str
    app_id: UUID
    deployment_id: UUID
    accepted_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempt_id = self.attempt_id

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        accepted_at = self.accepted_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempt_id": attempt_id,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "accepted_at": accepted_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attempt_id = d.pop("attempt_id")

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        deployment_submission_receipt = cls(
            attempt_id=attempt_id,
            app_id=app_id,
            deployment_id=deployment_id,
            accepted_at=accepted_at,
        )

        deployment_submission_receipt.additional_properties = d
        return deployment_submission_receipt

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
