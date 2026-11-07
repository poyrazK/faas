from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentPromotionReceipt")


@_attrs_define
class DeploymentPromotionReceipt:
    """Immutable acceptance of an original guarded 100 percent traffic promotion; current traffic and health are separate."""

    attempt_id: str
    request_hash: str
    app_id: UUID
    deployment_id: UUID
    expected_serving_deployment_id: str
    """Original serving guard; empty requires no serving sibling."""
    scope: str
    accepted_at: datetime.datetime
    project_id: UUID | Unset = UNSET
    environment_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempt_id = self.attempt_id

        request_hash = self.request_hash

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        expected_serving_deployment_id = self.expected_serving_deployment_id

        scope = self.scope

        accepted_at = self.accepted_at.isoformat()

        project_id: str | Unset = UNSET
        if not isinstance(self.project_id, Unset):
            project_id = str(self.project_id)

        environment_id: str | Unset = UNSET
        if not isinstance(self.environment_id, Unset):
            environment_id = str(self.environment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempt_id": attempt_id,
                "request_hash": request_hash,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "expected_serving_deployment_id": expected_serving_deployment_id,
                "scope": scope,
                "accepted_at": accepted_at,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attempt_id = d.pop("attempt_id")

        request_hash = d.pop("request_hash")

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        expected_serving_deployment_id = d.pop("expected_serving_deployment_id")

        scope = d.pop("scope")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        _project_id = d.pop("project_id", UNSET)
        project_id: UUID | Unset
        if isinstance(_project_id, Unset):
            project_id = UNSET
        else:
            project_id = UUID(_project_id)

        _environment_id = d.pop("environment_id", UNSET)
        environment_id: UUID | Unset
        if isinstance(_environment_id, Unset):
            environment_id = UNSET
        else:
            environment_id = UUID(_environment_id)

        deployment_promotion_receipt = cls(
            attempt_id=attempt_id,
            request_hash=request_hash,
            app_id=app_id,
            deployment_id=deployment_id,
            expected_serving_deployment_id=expected_serving_deployment_id,
            scope=scope,
            accepted_at=accepted_at,
            project_id=project_id,
            environment_id=environment_id,
        )

        deployment_promotion_receipt.additional_properties = d
        return deployment_promotion_receipt

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
