from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_grouping_explanation_basis import (
    IssueGroupingExplanationBasis,
    check_issue_grouping_explanation_basis,
)
from ..models.issue_grouping_explanation_discriminator_kind import (
    IssueGroupingExplanationDiscriminatorKind,
    check_issue_grouping_explanation_discriminator_kind,
)
from ..models.issue_grouping_explanation_strategy import (
    IssueGroupingExplanationStrategy,
    check_issue_grouping_explanation_strategy,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.issue_grouping_frame import IssueGroupingFrame


T = TypeVar("T", bound="IssueGroupingExplanation")


@_attrs_define
class IssueGroupingExplanation:
    """Read-only evidence reconstructed from a retained sanitized occurrence using the saved group version. Present only
    when its identity reproduces the saved fingerprint. Unsupported versions or mismatches omit the explanation.

    """

    grouping_version: int
    strategy: IssueGroupingExplanationStrategy
    basis: IssueGroupingExplanationBasis
    source_kind: str
    exception_type: str
    discriminator_kind: IssueGroupingExplanationDiscriminatorKind
    cause_index: int | Unset = UNSET
    """Zero-based selected cause index; absent when grouping uses the outer exception."""
    frames: list[IssueGroupingFrame] | Unset = UNSET
    route: str | Unset = UNSET
    http_status: int | Unset = UNSET
    discriminator_value: str | Unset = UNSET
    """Selected sanitized override, stable error code, or normalized message. May be empty for an empty message."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        grouping_version = self.grouping_version

        strategy: str = self.strategy

        basis: str = self.basis

        source_kind = self.source_kind

        exception_type = self.exception_type

        discriminator_kind: str = self.discriminator_kind

        cause_index = self.cause_index

        frames: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.frames, Unset):
            frames = []
            for frames_item_data in self.frames:
                frames_item = frames_item_data.to_dict()
                frames.append(frames_item)

        route = self.route

        http_status = self.http_status

        discriminator_value = self.discriminator_value

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "grouping_version": grouping_version,
                "strategy": strategy,
                "basis": basis,
                "source_kind": source_kind,
                "exception_type": exception_type,
                "discriminator_kind": discriminator_kind,
            }
        )
        if cause_index is not UNSET:
            field_dict["cause_index"] = cause_index
        if frames is not UNSET:
            field_dict["frames"] = frames
        if route is not UNSET:
            field_dict["route"] = route
        if http_status is not UNSET:
            field_dict["http_status"] = http_status
        if discriminator_value is not UNSET:
            field_dict["discriminator_value"] = discriminator_value

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_grouping_frame import IssueGroupingFrame

        d = dict(src_dict)
        grouping_version = d.pop("grouping_version")

        strategy = check_issue_grouping_explanation_strategy(d.pop("strategy"))

        basis = check_issue_grouping_explanation_basis(d.pop("basis"))

        source_kind = d.pop("source_kind")

        exception_type = d.pop("exception_type")

        discriminator_kind = check_issue_grouping_explanation_discriminator_kind(d.pop("discriminator_kind"))

        cause_index = d.pop("cause_index", UNSET)

        _frames = d.pop("frames", UNSET)
        frames: list[IssueGroupingFrame] | Unset = UNSET
        if _frames is not UNSET:
            frames = []
            for frames_item_data in _frames:
                frames_item = IssueGroupingFrame.from_dict(frames_item_data)

                frames.append(frames_item)

        route = d.pop("route", UNSET)

        http_status = d.pop("http_status", UNSET)

        discriminator_value = d.pop("discriminator_value", UNSET)

        issue_grouping_explanation = cls(
            grouping_version=grouping_version,
            strategy=strategy,
            basis=basis,
            source_kind=source_kind,
            exception_type=exception_type,
            discriminator_kind=discriminator_kind,
            cause_index=cause_index,
            frames=frames,
            route=route,
            http_status=http_status,
            discriminator_value=discriminator_value,
        )

        issue_grouping_explanation.additional_properties = d
        return issue_grouping_explanation

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
