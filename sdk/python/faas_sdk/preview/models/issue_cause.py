from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_cause_frame_order import IssueCauseFrameOrder, check_issue_cause_frame_order
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.issue_frame import IssueFrame


T = TypeVar("T", bound="IssueCause")


@_attrs_define
class IssueCause:
    """One sanitized linear cause, ordered immediate to deepest; bounds are UTF-8 bytes."""

    exception_type: str
    message: str
    error_code: str | Unset = UNSET
    frames: list[IssueFrame] | Unset = UNSET
    frame_order: IssueCauseFrameOrder | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        exception_type = self.exception_type

        message = self.message

        error_code = self.error_code

        frames: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.frames, Unset):
            frames = []
            for frames_item_data in self.frames:
                frames_item = frames_item_data.to_dict()
                frames.append(frames_item)

        frame_order: str | Unset = UNSET
        if not isinstance(self.frame_order, Unset):
            frame_order = self.frame_order

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "exception_type": exception_type,
                "message": message,
            }
        )
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if frames is not UNSET:
            field_dict["frames"] = frames
        if frame_order is not UNSET:
            field_dict["frame_order"] = frame_order

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_frame import IssueFrame

        d = dict(src_dict)
        exception_type = d.pop("exception_type")

        message = d.pop("message")

        error_code = d.pop("error_code", UNSET)

        _frames = d.pop("frames", UNSET)
        frames: list[IssueFrame] | Unset = UNSET
        if _frames is not UNSET:
            frames = []
            for frames_item_data in _frames:
                frames_item = IssueFrame.from_dict(frames_item_data)

                frames.append(frames_item)

        _frame_order = d.pop("frame_order", UNSET)
        frame_order: IssueCauseFrameOrder | Unset
        if isinstance(_frame_order, Unset):
            frame_order = UNSET
        else:
            frame_order = check_issue_cause_frame_order(_frame_order)

        issue_cause = cls(
            exception_type=exception_type,
            message=message,
            error_code=error_code,
            frames=frames,
            frame_order=frame_order,
        )

        issue_cause.additional_properties = d
        return issue_cause

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
