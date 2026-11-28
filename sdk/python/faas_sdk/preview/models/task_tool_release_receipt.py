from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.task_tool_release_receipt_pending_submission import (
    TaskToolReleaseReceiptPendingSubmission,
    check_task_tool_release_receipt_pending_submission,
)
from ..models.task_tool_release_receipt_stage import TaskToolReleaseReceiptStage, check_task_tool_release_receipt_stage
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.task_tool_release_receipt_parked import TaskToolReleaseReceiptParked
    from ..models.task_tool_release_receipt_previous_deployments import TaskToolReleaseReceiptPreviousDeployments


T = TypeVar("T", bound="TaskToolReleaseReceipt")


@_attrs_define
class TaskToolReleaseReceipt:
    """Monotonic checkpoint from the authenticated local execution claim holder. Deployment and worker identities require
    independent fresh native observation during recovery; this metadata grants no action authority.

    """

    revision: int
    release_id: str
    stage: TaskToolReleaseReceiptStage
    serving_captured: bool
    previous_deployments: TaskToolReleaseReceiptPreviousDeployments
    worker_ids: list[UUID]
    promoted: bool
    parked: TaskToolReleaseReceiptParked
    serving_deployment_id: UUID | Unset = UNSET
    web_deployment_id: UUID | Unset = UNSET
    worker_deployment_id: UUID | Unset = UNSET
    pending_submission: TaskToolReleaseReceiptPendingSubmission | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        release_id = self.release_id

        stage: str = self.stage

        serving_captured = self.serving_captured

        previous_deployments = self.previous_deployments.to_dict()

        worker_ids = []
        for worker_ids_item_data in self.worker_ids:
            worker_ids_item = str(worker_ids_item_data)
            worker_ids.append(worker_ids_item)

        promoted = self.promoted

        parked = self.parked.to_dict()

        serving_deployment_id: str | Unset = UNSET
        if not isinstance(self.serving_deployment_id, Unset):
            serving_deployment_id = str(self.serving_deployment_id)

        web_deployment_id: str | Unset = UNSET
        if not isinstance(self.web_deployment_id, Unset):
            web_deployment_id = str(self.web_deployment_id)

        worker_deployment_id: str | Unset = UNSET
        if not isinstance(self.worker_deployment_id, Unset):
            worker_deployment_id = str(self.worker_deployment_id)

        pending_submission: str | Unset = UNSET
        if not isinstance(self.pending_submission, Unset):
            pending_submission = self.pending_submission

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "revision": revision,
                "release_id": release_id,
                "stage": stage,
                "serving_captured": serving_captured,
                "previous_deployments": previous_deployments,
                "worker_ids": worker_ids,
                "promoted": promoted,
                "parked": parked,
            }
        )
        if serving_deployment_id is not UNSET:
            field_dict["serving_deployment_id"] = serving_deployment_id
        if web_deployment_id is not UNSET:
            field_dict["web_deployment_id"] = web_deployment_id
        if worker_deployment_id is not UNSET:
            field_dict["worker_deployment_id"] = worker_deployment_id
        if pending_submission is not UNSET:
            field_dict["pending_submission"] = pending_submission

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_release_receipt_parked import TaskToolReleaseReceiptParked
        from ..models.task_tool_release_receipt_previous_deployments import TaskToolReleaseReceiptPreviousDeployments

        d = dict(src_dict)
        revision = d.pop("revision")

        release_id = d.pop("release_id")

        stage = check_task_tool_release_receipt_stage(d.pop("stage"))

        serving_captured = d.pop("serving_captured")

        previous_deployments = TaskToolReleaseReceiptPreviousDeployments.from_dict(d.pop("previous_deployments"))

        worker_ids = []
        _worker_ids = d.pop("worker_ids")
        for worker_ids_item_data in _worker_ids:
            worker_ids_item = UUID(worker_ids_item_data)

            worker_ids.append(worker_ids_item)

        promoted = d.pop("promoted")

        parked = TaskToolReleaseReceiptParked.from_dict(d.pop("parked"))

        _serving_deployment_id = d.pop("serving_deployment_id", UNSET)
        serving_deployment_id: UUID | Unset
        if isinstance(_serving_deployment_id, Unset):
            serving_deployment_id = UNSET
        else:
            serving_deployment_id = UUID(_serving_deployment_id)

        _web_deployment_id = d.pop("web_deployment_id", UNSET)
        web_deployment_id: UUID | Unset
        if isinstance(_web_deployment_id, Unset):
            web_deployment_id = UNSET
        else:
            web_deployment_id = UUID(_web_deployment_id)

        _worker_deployment_id = d.pop("worker_deployment_id", UNSET)
        worker_deployment_id: UUID | Unset
        if isinstance(_worker_deployment_id, Unset):
            worker_deployment_id = UNSET
        else:
            worker_deployment_id = UUID(_worker_deployment_id)

        _pending_submission = d.pop("pending_submission", UNSET)
        pending_submission: TaskToolReleaseReceiptPendingSubmission | Unset
        if isinstance(_pending_submission, Unset):
            pending_submission = UNSET
        else:
            pending_submission = check_task_tool_release_receipt_pending_submission(_pending_submission)

        task_tool_release_receipt = cls(
            revision=revision,
            release_id=release_id,
            stage=stage,
            serving_captured=serving_captured,
            previous_deployments=previous_deployments,
            worker_ids=worker_ids,
            promoted=promoted,
            parked=parked,
            serving_deployment_id=serving_deployment_id,
            web_deployment_id=web_deployment_id,
            worker_deployment_id=worker_deployment_id,
            pending_submission=pending_submission,
        )

        return task_tool_release_receipt
