from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_policy_response_status import (
    WorkflowRecoveryPolicyResponseStatus,
    check_workflow_recovery_policy_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
    from ..models.workflow_recovery_policy_automatic_execution import WorkflowRecoveryPolicyAutomaticExecution
    from ..models.workflow_recovery_polling_notification import WorkflowRecoveryPollingNotification


T = TypeVar("T", bound="WorkflowRecoveryPolicyResponse")


@_attrs_define
class WorkflowRecoveryPolicyResponse:
    """Current recovery policy with trusted update actor and scheduler status. A versioned policy replacement resets its
    automatic failure pause and suppresses older pending automatic actions.

    """

    app_id: UUID
    version: int
    policy: WorkflowRecoveryPolicy
    """Explicit app recovery policy. Preview prepares parent-job plans and worker-discovered initial-failure
    reviews. initial_failure requires explicit failure statuses. Opt-in automatic_retry requires saved_failure only;
    automatic_initial requires initial_failure only. Both require explicit workflow and failure-status filters and
    execution limits. Resume count caps the absolute generation; preparation and execution budgets are separate.
    Cancellation remains manual."""
    updated_at: datetime.datetime
    actor_kind: str
    actor_id: str
    status: WorkflowRecoveryPolicyResponseStatus
    polling_notification: WorkflowRecoveryPollingNotification | Unset = UNSET
    """Last captured polling alert receipt. queued means durable outbox admission; no_receivers never replays to
    later subscribers. Successful polling retains the recovered receipt separately from the cleared error metadata.
   """
    automatic_execution: WorkflowRecoveryPolicyAutomaticExecution | Unset = UNSET
    """Durable consecutive failure counter and automatic execution pause for the current policy version. Updating
    the policy explicitly clears this pause."""
    last_evaluation_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        version = self.version

        policy = self.policy.to_dict()

        updated_at = self.updated_at.isoformat()

        actor_kind = self.actor_kind

        actor_id = self.actor_id

        status: str = self.status

        polling_notification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.polling_notification, Unset):
            polling_notification = self.polling_notification.to_dict()

        automatic_execution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.automatic_execution, Unset):
            automatic_execution = self.automatic_execution.to_dict()

        last_evaluation_at: str | Unset = UNSET
        if not isinstance(self.last_evaluation_at, Unset):
            last_evaluation_at = self.last_evaluation_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "version": version,
                "policy": policy,
                "updated_at": updated_at,
                "actor_kind": actor_kind,
                "actor_id": actor_id,
                "status": status,
            }
        )
        if polling_notification is not UNSET:
            field_dict["polling_notification"] = polling_notification
        if automatic_execution is not UNSET:
            field_dict["automatic_execution"] = automatic_execution
        if last_evaluation_at is not UNSET:
            field_dict["last_evaluation_at"] = last_evaluation_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_policy import WorkflowRecoveryPolicy
        from ..models.workflow_recovery_policy_automatic_execution import WorkflowRecoveryPolicyAutomaticExecution
        from ..models.workflow_recovery_polling_notification import WorkflowRecoveryPollingNotification

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        version = d.pop("version")

        policy = WorkflowRecoveryPolicy.from_dict(d.pop("policy"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        actor_kind = d.pop("actor_kind")

        actor_id = d.pop("actor_id")

        status = check_workflow_recovery_policy_response_status(d.pop("status"))

        _polling_notification = d.pop("polling_notification", UNSET)
        polling_notification: WorkflowRecoveryPollingNotification | Unset
        if isinstance(_polling_notification, Unset):
            polling_notification = UNSET
        else:
            polling_notification = WorkflowRecoveryPollingNotification.from_dict(_polling_notification)

        _automatic_execution = d.pop("automatic_execution", UNSET)
        automatic_execution: WorkflowRecoveryPolicyAutomaticExecution | Unset
        if isinstance(_automatic_execution, Unset):
            automatic_execution = UNSET
        else:
            automatic_execution = WorkflowRecoveryPolicyAutomaticExecution.from_dict(_automatic_execution)

        _last_evaluation_at = d.pop("last_evaluation_at", UNSET)
        last_evaluation_at: datetime.datetime | Unset
        if isinstance(_last_evaluation_at, Unset):
            last_evaluation_at = UNSET
        else:
            last_evaluation_at = datetime.datetime.fromisoformat(_last_evaluation_at)

        workflow_recovery_policy_response = cls(
            app_id=app_id,
            version=version,
            policy=policy,
            updated_at=updated_at,
            actor_kind=actor_kind,
            actor_id=actor_id,
            status=status,
            polling_notification=polling_notification,
            automatic_execution=automatic_execution,
            last_evaluation_at=last_evaluation_at,
        )

        workflow_recovery_policy_response.additional_properties = d
        return workflow_recovery_policy_response

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
