from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.application_standard_review_blocker import ApplicationStandardReviewBlocker
    from ..models.application_standard_review_request import ApplicationStandardReviewRequest
    from ..models.application_standard_reviewed_app import ApplicationStandardReviewedApp


T = TypeVar("T", bound="ApplicationStandardReview")


@_attrs_define
class ApplicationStandardReview:
    """Immutable saved preview. Its hash binds private authoritative inputs. Expiry does not prevent historical inspection;
    approval always requires fresh validation.

    """

    id: UUID
    org_id: UUID
    created_by: UUID
    request: ApplicationStandardReviewRequest
    """Explicit active and expected_revision are required, including false and zero. New assignments require
    active=true and revision=0."""
    approval_hash: str
    applications: list[ApplicationStandardReviewedApp]
    blockers: list[ApplicationStandardReviewBlocker]
    created_at: datetime.datetime
    expires_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        org_id = str(self.org_id)

        created_by = str(self.created_by)

        request = self.request.to_dict()

        approval_hash = self.approval_hash

        applications = []
        for applications_item_data in self.applications:
            applications_item = applications_item_data.to_dict()
            applications.append(applications_item)

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item = blockers_item_data.to_dict()
            blockers.append(blockers_item)

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "org_id": org_id,
                "created_by": created_by,
                "request": request,
                "approval_hash": approval_hash,
                "applications": applications,
                "blockers": blockers,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_review_blocker import ApplicationStandardReviewBlocker
        from ..models.application_standard_review_request import ApplicationStandardReviewRequest
        from ..models.application_standard_reviewed_app import ApplicationStandardReviewedApp

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        org_id = UUID(d.pop("org_id"))

        created_by = UUID(d.pop("created_by"))

        request = ApplicationStandardReviewRequest.from_dict(d.pop("request"))

        approval_hash = d.pop("approval_hash")

        applications = []
        _applications = d.pop("applications")
        for applications_item_data in _applications:
            applications_item = ApplicationStandardReviewedApp.from_dict(applications_item_data)

            applications.append(applications_item)

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = ApplicationStandardReviewBlocker.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        application_standard_review = cls(
            id=id,
            org_id=org_id,
            created_by=created_by,
            request=request,
            approval_hash=approval_hash,
            applications=applications,
            blockers=blockers,
            created_at=created_at,
            expires_at=expires_at,
        )

        return application_standard_review
