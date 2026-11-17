from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.hosted_task_source_intent_review_status import (
    HostedTaskSourceIntentReviewStatus,
    check_hosted_task_source_intent_review_status,
)
from ..models.hosted_task_source_intent_source_state import (
    HostedTaskSourceIntentSourceState,
    check_hosted_task_source_intent_source_state,
)
from ..models.hosted_task_source_intent_version import (
    HostedTaskSourceIntentVersion,
    check_hosted_task_source_intent_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.hosted_task_source_review import HostedTaskSourceReview


T = TypeVar("T", bound="HostedTaskSourceIntent")


@_attrs_define
class HostedTaskSourceIntent:
    """Immutable hosted source identity and last observed owner review metadata. Source review approval does not grant
    credential or execution authority. Expired ciphertext cannot be renewed under the same identity.

    """

    version: HostedTaskSourceIntentVersion
    id: str
    account_id: UUID
    requester_key_id: UUID
    intent_sha256: str
    review_sha256: str
    review: HostedTaskSourceReview
    """Canonical hosted source review, including source digests and native app baselines."""
    created_at: datetime.datetime
    expires_at: datetime.datetime
    source_state: HostedTaskSourceIntentSourceState
    ready_to_apply: bool
    """False because approval of a retained hosted source review is not permission to execute it."""
    bindings_verified: bool
    """False because storing authored Task source does not verify runtime credential bindings."""
    owner_authorization_required: bool
    """True until separate owner-controlled execution authorization and runtime credential qualification exist."""
    review_status: HostedTaskSourceIntentReviewStatus | Unset = UNSET
    """Last recorded hosted source review state; absent on older receipts means pending until source expiry."""
    decided_at: datetime.datetime | Unset = UNSET
    """Original hosted source owner decision time, retained unchanged by exact repeats."""

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        id = self.id

        account_id = str(self.account_id)

        requester_key_id = str(self.requester_key_id)

        intent_sha256 = self.intent_sha256

        review_sha256 = self.review_sha256

        review = self.review.to_dict()

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        source_state: str = self.source_state

        ready_to_apply = self.ready_to_apply

        bindings_verified = self.bindings_verified

        owner_authorization_required = self.owner_authorization_required

        review_status: str | Unset = UNSET
        if not isinstance(self.review_status, Unset):
            review_status = self.review_status

        decided_at: str | Unset = UNSET
        if not isinstance(self.decided_at, Unset):
            decided_at = self.decided_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "id": id,
                "account_id": account_id,
                "requester_key_id": requester_key_id,
                "intent_sha256": intent_sha256,
                "review_sha256": review_sha256,
                "review": review,
                "created_at": created_at,
                "expires_at": expires_at,
                "source_state": source_state,
                "ready_to_apply": ready_to_apply,
                "bindings_verified": bindings_verified,
                "owner_authorization_required": owner_authorization_required,
            }
        )
        if review_status is not UNSET:
            field_dict["review_status"] = review_status
        if decided_at is not UNSET:
            field_dict["decided_at"] = decided_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.hosted_task_source_review import HostedTaskSourceReview

        d = dict(src_dict)
        version = check_hosted_task_source_intent_version(d.pop("version"))

        id = d.pop("id")

        account_id = UUID(d.pop("account_id"))

        requester_key_id = UUID(d.pop("requester_key_id"))

        intent_sha256 = d.pop("intent_sha256")

        review_sha256 = d.pop("review_sha256")

        review = HostedTaskSourceReview.from_dict(d.pop("review"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        source_state = check_hosted_task_source_intent_source_state(d.pop("source_state"))

        ready_to_apply = d.pop("ready_to_apply")

        bindings_verified = d.pop("bindings_verified")

        owner_authorization_required = d.pop("owner_authorization_required")

        _review_status = d.pop("review_status", UNSET)
        review_status: HostedTaskSourceIntentReviewStatus | Unset
        if isinstance(_review_status, Unset):
            review_status = UNSET
        else:
            review_status = check_hosted_task_source_intent_review_status(_review_status)

        _decided_at = d.pop("decided_at", UNSET)
        decided_at: datetime.datetime | Unset
        if isinstance(_decided_at, Unset):
            decided_at = UNSET
        else:
            decided_at = datetime.datetime.fromisoformat(_decided_at)

        hosted_task_source_intent = cls(
            version=version,
            id=id,
            account_id=account_id,
            requester_key_id=requester_key_id,
            intent_sha256=intent_sha256,
            review_sha256=review_sha256,
            review=review,
            created_at=created_at,
            expires_at=expires_at,
            source_state=source_state,
            ready_to_apply=ready_to_apply,
            bindings_verified=bindings_verified,
            owner_authorization_required=owner_authorization_required,
            review_status=review_status,
            decided_at=decided_at,
        )

        return hosted_task_source_intent
