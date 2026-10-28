from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_submission_receipt_status import (
    ProjectSubmissionReceiptStatus,
    check_project_submission_receipt_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.apply_response import ApplyResponse


T = TypeVar("T", bound="ProjectSubmissionReceipt")


@_attrs_define
class ProjectSubmissionReceipt:
    """Durable, account-scoped recovery for one project apply attempt."""

    selection_complete: bool
    """Whether the selected workload set was durably recorded before enqueue."""
    attempt_id: str
    project_slug: str
    status: ProjectSubmissionReceiptStatus
    apply: ApplyResponse
    """Apply response. Carries the inserted project_id and per-app IDs."""
    environment: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        selection_complete = self.selection_complete

        attempt_id = self.attempt_id

        project_slug = self.project_slug

        status: str = self.status

        apply = self.apply.to_dict()

        environment = self.environment

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "selection_complete": selection_complete,
                "attempt_id": attempt_id,
                "project_slug": project_slug,
                "status": status,
                "apply": apply,
            }
        )
        if environment is not UNSET:
            field_dict["environment"] = environment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.apply_response import ApplyResponse

        d = dict(src_dict)
        selection_complete = d.pop("selection_complete")

        attempt_id = d.pop("attempt_id")

        project_slug = d.pop("project_slug")

        status = check_project_submission_receipt_status(d.pop("status"))

        apply = ApplyResponse.from_dict(d.pop("apply"))

        environment = d.pop("environment", UNSET)

        project_submission_receipt = cls(
            selection_complete=selection_complete,
            attempt_id=attempt_id,
            project_slug=project_slug,
            status=status,
            apply=apply,
            environment=environment,
        )

        project_submission_receipt.additional_properties = d
        return project_submission_receipt

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
