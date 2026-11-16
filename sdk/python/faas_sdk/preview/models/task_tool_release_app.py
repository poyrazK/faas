from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.task_tool_release_app_role import TaskToolReleaseAppRole, check_task_tool_release_app_role

if TYPE_CHECKING:
    from ..models.app_release_review import AppReleaseReview


T = TypeVar("T", bound="TaskToolReleaseApp")


@_attrs_define
class TaskToolReleaseApp:
    """One existing personal app role and its exact default-environment native release snapshot."""

    role: TaskToolReleaseAppRole
    app: str
    review: AppReleaseReview
    """Atomic app configuration and serving-deployment identities used to fence release admission."""

    def to_dict(self) -> dict[str, Any]:
        role: str = self.role

        app = self.app

        review = self.review.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "role": role,
                "app": app,
                "review": review,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_release_review import AppReleaseReview

        d = dict(src_dict)
        role = check_task_tool_release_app_role(d.pop("role"))

        app = d.pop("app")

        review = AppReleaseReview.from_dict(d.pop("review"))

        task_tool_release_app = cls(
            role=role,
            app=app,
            review=review,
        )

        return task_tool_release_app
