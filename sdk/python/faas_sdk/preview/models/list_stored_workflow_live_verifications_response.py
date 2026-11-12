from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.stored_workflow_live_verification import StoredWorkflowLiveVerification


T = TypeVar("T", bound="ListStoredWorkflowLiveVerificationsResponse")


@_attrs_define
class ListStoredWorkflowLiveVerificationsResponse:
    """Current target identities and the twenty most recent retained live verifications."""

    published_version: int
    verifications: list[StoredWorkflowLiveVerification]
    deployment_id: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        published_version = self.published_version

        verifications = []
        for verifications_item_data in self.verifications:
            verifications_item = verifications_item_data.to_dict()
            verifications.append(verifications_item)

        deployment_id = self.deployment_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "published_version": published_version,
                "verifications": verifications,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.stored_workflow_live_verification import StoredWorkflowLiveVerification

        d = dict(src_dict)
        published_version = d.pop("published_version")

        verifications = []
        _verifications = d.pop("verifications")
        for verifications_item_data in _verifications:
            verifications_item = StoredWorkflowLiveVerification.from_dict(verifications_item_data)

            verifications.append(verifications_item)

        deployment_id = d.pop("deployment_id", UNSET)

        list_stored_workflow_live_verifications_response = cls(
            published_version=published_version,
            verifications=verifications,
            deployment_id=deployment_id,
        )

        return list_stored_workflow_live_verifications_response
