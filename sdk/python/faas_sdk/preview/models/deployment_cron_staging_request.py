from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.deployment_cron_staging_request_operation import (
    DeploymentCronStagingRequestOperation,
    check_deployment_cron_staging_request_operation,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.deployment_cron_config import DeploymentCronConfig


T = TypeVar("T", bound="DeploymentCronStagingRequest")


@_attrs_define
class DeploymentCronStagingRequest:
    """Immutable HTTP cron intent. Prepare retains active schedules until the submitted production release owns all
    traffic; cancel fences an unsubmitted intent. Stage and restore support legacy recovery.

    """

    session_id: str
    operation: DeploymentCronStagingRequestOperation
    expected_app_id: UUID
    expected_deployment_id: str
    expected_revision: int
    before: list[DeploymentCronConfig]
    desired: list[DeploymentCronConfig]
    upload_id: str | Unset = UNSET
    """Owned opaque upload identity to track for autonomous committed-upload recovery."""

    def to_dict(self) -> dict[str, Any]:
        session_id = self.session_id

        operation: str = self.operation

        expected_app_id = str(self.expected_app_id)

        expected_deployment_id = self.expected_deployment_id

        expected_revision = self.expected_revision

        before = []
        for before_item_data in self.before:
            before_item = before_item_data.to_dict()
            before.append(before_item)

        desired = []
        for desired_item_data in self.desired:
            desired_item = desired_item_data.to_dict()
            desired.append(desired_item)

        upload_id = self.upload_id

        field_dict: dict[str, Any] = {}

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
        if upload_id is not UNSET:
            field_dict["upload_id"] = upload_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_cron_config import DeploymentCronConfig

        d = dict(src_dict)
        session_id = d.pop("session_id")

        operation = check_deployment_cron_staging_request_operation(d.pop("operation"))

        expected_app_id = UUID(d.pop("expected_app_id"))

        expected_deployment_id = d.pop("expected_deployment_id")

        expected_revision = d.pop("expected_revision")

        before = []
        _before = d.pop("before")
        for before_item_data in _before:
            before_item = DeploymentCronConfig.from_dict(before_item_data)

            before.append(before_item)

        desired = []
        _desired = d.pop("desired")
        for desired_item_data in _desired:
            desired_item = DeploymentCronConfig.from_dict(desired_item_data)

            desired.append(desired_item)

        upload_id = d.pop("upload_id", UNSET)

        deployment_cron_staging_request = cls(
            session_id=session_id,
            operation=operation,
            expected_app_id=expected_app_id,
            expected_deployment_id=expected_deployment_id,
            expected_revision=expected_revision,
            before=before,
            desired=desired,
            upload_id=upload_id,
        )

        return deployment_cron_staging_request
