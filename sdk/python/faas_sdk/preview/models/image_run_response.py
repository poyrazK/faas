from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.image_run_response_image_status import ImageRunResponseImageStatus, check_image_run_response_image_status
from ..models.image_run_response_status import ImageRunResponseStatus, check_image_run_response_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_task_attach_info import AppTaskAttachInfo


T = TypeVar("T", bound="ImageRunResponse")


@_attrs_define
class ImageRunResponse:
    """Answer to POST /v1/image-runs. job names the runner job; run_id addresses /v1/jobs/{job}/runs/{run_id} once status
    is queued.

    """

    status: ImageRunResponseStatus
    job: str
    """Runner job name (ir-<hash>)."""
    image: str
    """Normalized image reference."""
    image_status: ImageRunResponseImageStatus
    image_error: str | Unset = UNSET
    """Last pull or build error while the image is retried."""
    image_digest: str | Unset = UNSET
    """Resolved manifest digest once ready."""
    run_id: UUID | Unset = UNSET
    command: list[str] | Unset = UNSET
    """The resolved argv the task runs."""
    attach: AppTaskAttachInfo | Unset = UNSET
    """One-time attach credential for an interactive app task. Returned only in the create response."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        job = self.job

        image = self.image

        image_status: str = self.image_status

        image_error = self.image_error

        image_digest = self.image_digest

        run_id: str | Unset = UNSET
        if not isinstance(self.run_id, Unset):
            run_id = str(self.run_id)

        command: list[str] | Unset = UNSET
        if not isinstance(self.command, Unset):
            command = self.command

        attach: dict[str, Any] | Unset = UNSET
        if not isinstance(self.attach, Unset):
            attach = self.attach.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "job": job,
                "image": image,
                "image_status": image_status,
            }
        )
        if image_error is not UNSET:
            field_dict["image_error"] = image_error
        if image_digest is not UNSET:
            field_dict["image_digest"] = image_digest
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if command is not UNSET:
            field_dict["command"] = command
        if attach is not UNSET:
            field_dict["attach"] = attach

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_task_attach_info import AppTaskAttachInfo

        d = dict(src_dict)
        status = check_image_run_response_status(d.pop("status"))

        job = d.pop("job")

        image = d.pop("image")

        image_status = check_image_run_response_image_status(d.pop("image_status"))

        image_error = d.pop("image_error", UNSET)

        image_digest = d.pop("image_digest", UNSET)

        _run_id = d.pop("run_id", UNSET)
        run_id: UUID | Unset
        if isinstance(_run_id, Unset):
            run_id = UNSET
        else:
            run_id = UUID(_run_id)

        command = cast(list[str], d.pop("command", UNSET))

        _attach = d.pop("attach", UNSET)
        attach: AppTaskAttachInfo | Unset
        if isinstance(_attach, Unset):
            attach = UNSET
        else:
            attach = AppTaskAttachInfo.from_dict(_attach)

        image_run_response = cls(
            status=status,
            job=job,
            image=image,
            image_status=image_status,
            image_error=image_error,
            image_digest=image_digest,
            run_id=run_id,
            command=command,
            attach=attach,
        )

        image_run_response.additional_properties = d
        return image_run_response

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
