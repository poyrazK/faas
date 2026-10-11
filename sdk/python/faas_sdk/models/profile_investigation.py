from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_investigation_input import ProfileInvestigationInput
    from ..models.profile_regression_assessment import ProfileRegressionAssessment


T = TypeVar("T", bound="ProfileInvestigation")


@_attrs_define
class ProfileInvestigation:
    """Persistent app-owned selections and notes; no profile samples are stored. Up to 50 investigations per app."""

    id: UUID
    app_id: UUID
    revision: int
    investigation: ProfileInvestigationInput
    """Saved comparison metadata. Title allows 160 UTF-8 bytes and findings and notes 8192 bytes each. Selections
    share a runtime. Changed windows must belong to retained deployments; unchanged expired windows permit
    commentary edits."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    assessment: ProfileRegressionAssessment | Unset = UNSET
    """Latest bounded historical CPU assessment, at most 64 KiB JSON, including at most 32 KiB evidence. Original
    samples are not retained. A different current saved revision marks this assessment stale. Profile expiry leaves
    this historical summary readable, without extending backend retention."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        revision = self.revision

        investigation = self.investigation.to_dict()

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        assessment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.assessment, Unset):
            assessment = self.assessment.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "revision": revision,
                "investigation": investigation,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if assessment is not UNSET:
            field_dict["assessment"] = assessment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_investigation_input import ProfileInvestigationInput
        from ..models.profile_regression_assessment import ProfileRegressionAssessment

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        revision = d.pop("revision")

        investigation = ProfileInvestigationInput.from_dict(d.pop("investigation"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _assessment = d.pop("assessment", UNSET)
        assessment: ProfileRegressionAssessment | Unset
        if isinstance(_assessment, Unset):
            assessment = UNSET
        else:
            assessment = ProfileRegressionAssessment.from_dict(_assessment)

        profile_investigation = cls(
            id=id,
            app_id=app_id,
            revision=revision,
            investigation=investigation,
            created_at=created_at,
            updated_at=updated_at,
            assessment=assessment,
        )

        profile_investigation.additional_properties = d
        return profile_investigation

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
