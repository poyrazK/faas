from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.application_standard_review_request_scope import (
    ApplicationStandardReviewRequestScope,
    check_application_standard_review_request_scope,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplicationStandardReviewRequest")


@_attrs_define
class ApplicationStandardReviewRequest:
    """Explicit active and expected_revision are required, including false and zero. New assignments require active=true
    and revision=0.

    """

    scope: ApplicationStandardReviewRequestScope
    scope_id: UUID
    standard_id: UUID
    admission_version: int
    expected_revision: int
    active: bool
    batch_size: int
    assignment_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

        scope_id = str(self.scope_id)

        standard_id = str(self.standard_id)

        admission_version = self.admission_version

        expected_revision = self.expected_revision

        active = self.active

        batch_size = self.batch_size

        assignment_id: str | Unset = UNSET
        if not isinstance(self.assignment_id, Unset):
            assignment_id = str(self.assignment_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "scope": scope,
                "scope_id": scope_id,
                "standard_id": standard_id,
                "admission_version": admission_version,
                "expected_revision": expected_revision,
                "active": active,
                "batch_size": batch_size,
            }
        )
        if assignment_id is not UNSET:
            field_dict["assignment_id"] = assignment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scope = check_application_standard_review_request_scope(d.pop("scope"))

        scope_id = UUID(d.pop("scope_id"))

        standard_id = UUID(d.pop("standard_id"))

        admission_version = d.pop("admission_version")

        expected_revision = d.pop("expected_revision")

        active = d.pop("active")

        batch_size = d.pop("batch_size")

        _assignment_id = d.pop("assignment_id", UNSET)
        assignment_id: UUID | Unset
        if isinstance(_assignment_id, Unset):
            assignment_id = UNSET
        else:
            assignment_id = UUID(_assignment_id)

        application_standard_review_request = cls(
            scope=scope,
            scope_id=scope_id,
            standard_id=standard_id,
            admission_version=admission_version,
            expected_revision=expected_revision,
            active=active,
            batch_size=batch_size,
            assignment_id=assignment_id,
        )

        return application_standard_review_request
