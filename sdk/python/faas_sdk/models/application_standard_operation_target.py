from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.application_standard_operation_target_state import (
    ApplicationStandardOperationTargetState,
    check_application_standard_operation_target_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.application_standard_reviewed_app import ApplicationStandardReviewedApp


T = TypeVar("T", bound="ApplicationStandardOperationTarget")


@_attrs_define
class ApplicationStandardOperationTarget:
    """One application target and its installation and observation progress in a controlled rollout."""

    app_id: UUID
    position: int
    approved_app: ApplicationStandardReviewedApp
    """Application inputs, adoption pins and resolved changes captured in a saved review."""
    state: ApplicationStandardOperationTargetState
    desired_revision: int
    updated_at: datetime.datetime
    error_code: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        position = self.position

        approved_app = self.approved_app.to_dict()

        state: str = self.state

        desired_revision = self.desired_revision

        updated_at = self.updated_at.isoformat()

        error_code = self.error_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "position": position,
                "approved_app": approved_app,
                "state": state,
                "desired_revision": desired_revision,
                "updated_at": updated_at,
            }
        )
        if error_code is not UNSET:
            field_dict["error_code"] = error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_reviewed_app import ApplicationStandardReviewedApp

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        position = d.pop("position")

        approved_app = ApplicationStandardReviewedApp.from_dict(d.pop("approved_app"))

        state = check_application_standard_operation_target_state(d.pop("state"))

        desired_revision = d.pop("desired_revision")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        error_code = d.pop("error_code", UNSET)

        application_standard_operation_target = cls(
            app_id=app_id,
            position=position,
            approved_app=approved_app,
            state=state,
            desired_revision=desired_revision,
            updated_at=updated_at,
            error_code=error_code,
        )

        return application_standard_operation_target
