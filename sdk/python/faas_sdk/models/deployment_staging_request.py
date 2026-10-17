from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_staging_request_operation import (
    DeploymentStagingRequestOperation,
    check_deployment_staging_request_operation,
)

if TYPE_CHECKING:
    from ..models.deployment_staging_request_before import DeploymentStagingRequestBefore
    from ..models.deployment_staging_request_desired import DeploymentStagingRequestDesired


T = TypeVar("T", bound="DeploymentStagingRequest")


@_attrs_define
class DeploymentStagingRequest:
    """The immutable non-secret intent used to stage or restore deployment policies."""

    session_id: str
    operation: DeploymentStagingRequestOperation
    expected_app_id: UUID
    expected_deployment_id: str
    """The captured latest deployment ID, or an empty string when none exists."""
    expected_revision: int
    before: DeploymentStagingRequestBefore
    desired: DeploymentStagingRequestDesired
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        session_id = self.session_id

        operation: str = self.operation

        expected_app_id = str(self.expected_app_id)

        expected_deployment_id = self.expected_deployment_id

        expected_revision = self.expected_revision

        before = self.before.to_dict()

        desired = self.desired.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "session_id": session_id,
                "operation": operation,
                "expected_app_id": expected_app_id,
                "expected_deployment_id": expected_deployment_id,
                "expected_revision": expected_revision,
                "before": before,
                "desired": desired,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_staging_request_before import DeploymentStagingRequestBefore
        from ..models.deployment_staging_request_desired import DeploymentStagingRequestDesired

        d = dict(src_dict)
        session_id = d.pop("session_id")

        operation = check_deployment_staging_request_operation(d.pop("operation"))

        expected_app_id = UUID(d.pop("expected_app_id"))

        expected_deployment_id = d.pop("expected_deployment_id")

        expected_revision = d.pop("expected_revision")

        before = DeploymentStagingRequestBefore.from_dict(d.pop("before"))

        desired = DeploymentStagingRequestDesired.from_dict(d.pop("desired"))

        deployment_staging_request = cls(
            session_id=session_id,
            operation=operation,
            expected_app_id=expected_app_id,
            expected_deployment_id=expected_deployment_id,
            expected_revision=expected_revision,
            before=before,
            desired=desired,
        )

        deployment_staging_request.additional_properties = d
        return deployment_staging_request

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
