from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_progress_disposition import (
    OperationWorkflowPlaybookProgressDisposition,
    check_operation_workflow_playbook_progress_disposition,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook import OperationWorkflowPlaybook
    from ..models.operation_workflow_playbook_applicability import OperationWorkflowPlaybookApplicability
    from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome
    from ..models.operation_workflow_playbook_outcome_follow_up import OperationWorkflowPlaybookOutcomeFollowUp
    from ..models.operation_workflow_playbook_progress_change import OperationWorkflowPlaybookProgressChange
    from ..models.operation_workflow_playbook_progress_item import OperationWorkflowPlaybookProgressItem
    from ..models.operation_workflow_playbook_review import OperationWorkflowPlaybookReview


T = TypeVar("T", bound="OperationWorkflowPlaybookProgress")


@_attrs_define
class OperationWorkflowPlaybookProgress:
    """OperationWorkflowPlaybookProgress. Durable advisory checklist coordination, independent of application business
    success.

    """

    id: UUID
    """OperationWorkflowPlaybookProgress.id. Stable checklist identity; repeated starts for the same pinned case
    and definition return the existing record."""
    revision: int
    """OperationWorkflowPlaybookProgress.revision. Optimistic concurrency revision."""
    playbook: OperationWorkflowPlaybook
    """OperationWorkflowPlaybook. Application-owned advisory guidance; evidence requirements are human checklists
    and links are never executed by Gregale."""
    contract_version: int
    """OperationWorkflowPlaybookProgress.contract_version. Pinned workflow contract revision."""
    definition_id: UUID
    """OperationWorkflowPlaybookProgress.definition_id. Immutable original reporting definition."""
    reporting_operation_id: UUID
    """OperationWorkflowPlaybookProgress.reporting_operation_id. Original reporting operation used when the
    checklist started."""
    items: list[OperationWorkflowPlaybookProgressItem]
    """OperationWorkflowPlaybookProgress.items. Frozen evidence labels and their latest review records."""
    completed_count: int
    """OperationWorkflowPlaybookProgress.completed_count. Number of checklist items reviewed as completed."""
    checklist_complete: bool
    """OperationWorkflowPlaybookProgress.checklist_complete. Every item in a nonempty checklist is completed;
    conveys no application readiness or business outcome."""
    created_at: datetime.datetime
    """OperationWorkflowPlaybookProgress.created_at. Server start clock."""
    created_by: str
    """OperationWorkflowPlaybookProgress.created_by. Authenticated checklist creator."""
    updated_at: datetime.datetime
    """OperationWorkflowPlaybookProgress.updated_at. Latest review clock."""
    history: list[OperationWorkflowPlaybookProgressChange]
    """OperationWorkflowPlaybookProgress.history. Most recent 64 committed item review snapshots, in revision
    order."""
    history_truncated: bool
    """OperationWorkflowPlaybookProgress.history_truncated. Older review changes were pruned from the bounded
    history."""
    outcome_follow_ups: list[OperationWorkflowPlaybookOutcomeFollowUp] | Unset = UNSET
    """OperationWorkflowPlaybookProgress.outcome_follow_ups. Human-reviewed outcome-specific follow-up lists;
    original checklist counts and business reports remain independent."""
    outcome_deadline_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowPlaybookProgress.outcome_deadline_at. Immutable checklist creation time plus the pinned
    application wait duration; human edits and watch enrollment do not reschedule it."""
    start_state_revision: int | Unset = UNSET
    """OperationWorkflowPlaybookProgress.start_state_revision. Original case state revision pinned when a playbook
    with an expected outcome starts; earlier reports cannot establish its business result."""
    business_outcome: OperationWorkflowPlaybookBusinessOutcome | Unset = UNSET
    """OperationWorkflowPlaybookBusinessOutcome. Exact-case application business result compared with a frozen
    playbook expectation. Independent of human completion and evidence satisfaction; changed guidance never silently
    changes the expected outcome."""
    disposition: OperationWorkflowPlaybookProgressDisposition | Unset = UNSET
    """OperationWorkflowPlaybookProgress.disposition. Coordination disposition. Omitted legacy values mean active.
    Closed checklists reject item edits and further decisions; this is not business success."""
    latest_review: OperationWorkflowPlaybookReview | Unset = UNSET
    """OperationWorkflowPlaybookReview. Durable human choice with server reviewer, observation context and
    immutable replacement reference. Not an application outcome or execution permission."""
    reviews: list[OperationWorkflowPlaybookReview] | Unset = UNSET
    """OperationWorkflowPlaybookProgress.reviews. Most recent 32 durable review decisions in shared revision order;
    independent of item review history."""
    reviews_truncated: bool | Unset = UNSET
    """OperationWorkflowPlaybookProgress.reviews_truncated. Older human review decisions were pruned from the
    bounded journal."""
    replaces_progress_ids: list[UUID] | Unset = UNSET
    """OperationWorkflowPlaybookProgress.replaces_progress_ids. Case-read-only inverse links from replaced
    predecessors. Derived from retained exact-case records and never persisted or accepted in requests."""
    applicability: OperationWorkflowPlaybookApplicability | Unset = UNSET
    """OperationWorkflowPlaybookApplicability. Ephemeral applicability of a durable pinned checklist against
    current exact-case evidence. No progress, review history, readiness or outcome is mutated."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        revision = self.revision

        playbook = self.playbook.to_dict()

        contract_version = self.contract_version

        definition_id = str(self.definition_id)

        reporting_operation_id = str(self.reporting_operation_id)

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        completed_count = self.completed_count

        checklist_complete = self.checklist_complete

        created_at = self.created_at.isoformat()

        created_by = self.created_by

        updated_at = self.updated_at.isoformat()

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        history_truncated = self.history_truncated

        outcome_follow_ups: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.outcome_follow_ups, Unset):
            outcome_follow_ups = []
            for outcome_follow_ups_item_data in self.outcome_follow_ups:
                outcome_follow_ups_item = outcome_follow_ups_item_data.to_dict()
                outcome_follow_ups.append(outcome_follow_ups_item)

        outcome_deadline_at: str | Unset = UNSET
        if not isinstance(self.outcome_deadline_at, Unset):
            outcome_deadline_at = self.outcome_deadline_at.isoformat()

        start_state_revision = self.start_state_revision

        business_outcome: dict[str, Any] | Unset = UNSET
        if not isinstance(self.business_outcome, Unset):
            business_outcome = self.business_outcome.to_dict()

        disposition: str | Unset = UNSET
        if not isinstance(self.disposition, Unset):
            disposition = self.disposition

        latest_review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.latest_review, Unset):
            latest_review = self.latest_review.to_dict()

        reviews: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.reviews, Unset):
            reviews = []
            for reviews_item_data in self.reviews:
                reviews_item = reviews_item_data.to_dict()
                reviews.append(reviews_item)

        reviews_truncated = self.reviews_truncated

        replaces_progress_ids: list[str] | Unset = UNSET
        if not isinstance(self.replaces_progress_ids, Unset):
            replaces_progress_ids = []
            for replaces_progress_ids_item_data in self.replaces_progress_ids:
                replaces_progress_ids_item = str(replaces_progress_ids_item_data)
                replaces_progress_ids.append(replaces_progress_ids_item)

        applicability: dict[str, Any] | Unset = UNSET
        if not isinstance(self.applicability, Unset):
            applicability = self.applicability.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "revision": revision,
                "playbook": playbook,
                "contract_version": contract_version,
                "definition_id": definition_id,
                "reporting_operation_id": reporting_operation_id,
                "items": items,
                "completed_count": completed_count,
                "checklist_complete": checklist_complete,
                "created_at": created_at,
                "created_by": created_by,
                "updated_at": updated_at,
                "history": history,
                "history_truncated": history_truncated,
            }
        )
        if outcome_follow_ups is not UNSET:
            field_dict["outcome_follow_ups"] = outcome_follow_ups
        if outcome_deadline_at is not UNSET:
            field_dict["outcome_deadline_at"] = outcome_deadline_at
        if start_state_revision is not UNSET:
            field_dict["start_state_revision"] = start_state_revision
        if business_outcome is not UNSET:
            field_dict["business_outcome"] = business_outcome
        if disposition is not UNSET:
            field_dict["disposition"] = disposition
        if latest_review is not UNSET:
            field_dict["latest_review"] = latest_review
        if reviews is not UNSET:
            field_dict["reviews"] = reviews
        if reviews_truncated is not UNSET:
            field_dict["reviews_truncated"] = reviews_truncated
        if replaces_progress_ids is not UNSET:
            field_dict["replaces_progress_ids"] = replaces_progress_ids
        if applicability is not UNSET:
            field_dict["applicability"] = applicability

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook import OperationWorkflowPlaybook
        from ..models.operation_workflow_playbook_applicability import OperationWorkflowPlaybookApplicability
        from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome
        from ..models.operation_workflow_playbook_outcome_follow_up import OperationWorkflowPlaybookOutcomeFollowUp
        from ..models.operation_workflow_playbook_progress_change import OperationWorkflowPlaybookProgressChange
        from ..models.operation_workflow_playbook_progress_item import OperationWorkflowPlaybookProgressItem
        from ..models.operation_workflow_playbook_review import OperationWorkflowPlaybookReview

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        revision = d.pop("revision")

        playbook = OperationWorkflowPlaybook.from_dict(d.pop("playbook"))

        contract_version = d.pop("contract_version")

        definition_id = UUID(d.pop("definition_id"))

        reporting_operation_id = UUID(d.pop("reporting_operation_id"))

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OperationWorkflowPlaybookProgressItem.from_dict(items_item_data)

            items.append(items_item)

        completed_count = d.pop("completed_count")

        checklist_complete = d.pop("checklist_complete")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        created_by = d.pop("created_by")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = OperationWorkflowPlaybookProgressChange.from_dict(history_item_data)

            history.append(history_item)

        history_truncated = d.pop("history_truncated")

        _outcome_follow_ups = d.pop("outcome_follow_ups", UNSET)
        outcome_follow_ups: list[OperationWorkflowPlaybookOutcomeFollowUp] | Unset = UNSET
        if _outcome_follow_ups is not UNSET:
            outcome_follow_ups = []
            for outcome_follow_ups_item_data in _outcome_follow_ups:
                outcome_follow_ups_item = OperationWorkflowPlaybookOutcomeFollowUp.from_dict(
                    outcome_follow_ups_item_data
                )

                outcome_follow_ups.append(outcome_follow_ups_item)

        _outcome_deadline_at = d.pop("outcome_deadline_at", UNSET)
        outcome_deadline_at: datetime.datetime | Unset
        if isinstance(_outcome_deadline_at, Unset):
            outcome_deadline_at = UNSET
        else:
            outcome_deadline_at = datetime.datetime.fromisoformat(_outcome_deadline_at)

        start_state_revision = d.pop("start_state_revision", UNSET)

        _business_outcome = d.pop("business_outcome", UNSET)
        business_outcome: OperationWorkflowPlaybookBusinessOutcome | Unset
        if isinstance(_business_outcome, Unset):
            business_outcome = UNSET
        else:
            business_outcome = OperationWorkflowPlaybookBusinessOutcome.from_dict(_business_outcome)

        _disposition = d.pop("disposition", UNSET)
        disposition: OperationWorkflowPlaybookProgressDisposition | Unset
        if isinstance(_disposition, Unset):
            disposition = UNSET
        else:
            disposition = check_operation_workflow_playbook_progress_disposition(_disposition)

        _latest_review = d.pop("latest_review", UNSET)
        latest_review: OperationWorkflowPlaybookReview | Unset
        if isinstance(_latest_review, Unset):
            latest_review = UNSET
        else:
            latest_review = OperationWorkflowPlaybookReview.from_dict(_latest_review)

        _reviews = d.pop("reviews", UNSET)
        reviews: list[OperationWorkflowPlaybookReview] | Unset = UNSET
        if _reviews is not UNSET:
            reviews = []
            for reviews_item_data in _reviews:
                reviews_item = OperationWorkflowPlaybookReview.from_dict(reviews_item_data)

                reviews.append(reviews_item)

        reviews_truncated = d.pop("reviews_truncated", UNSET)

        _replaces_progress_ids = d.pop("replaces_progress_ids", UNSET)
        replaces_progress_ids: list[UUID] | Unset = UNSET
        if _replaces_progress_ids is not UNSET:
            replaces_progress_ids = []
            for replaces_progress_ids_item_data in _replaces_progress_ids:
                replaces_progress_ids_item = UUID(replaces_progress_ids_item_data)

                replaces_progress_ids.append(replaces_progress_ids_item)

        _applicability = d.pop("applicability", UNSET)
        applicability: OperationWorkflowPlaybookApplicability | Unset
        if isinstance(_applicability, Unset):
            applicability = UNSET
        else:
            applicability = OperationWorkflowPlaybookApplicability.from_dict(_applicability)

        operation_workflow_playbook_progress = cls(
            id=id,
            revision=revision,
            playbook=playbook,
            contract_version=contract_version,
            definition_id=definition_id,
            reporting_operation_id=reporting_operation_id,
            items=items,
            completed_count=completed_count,
            checklist_complete=checklist_complete,
            created_at=created_at,
            created_by=created_by,
            updated_at=updated_at,
            history=history,
            history_truncated=history_truncated,
            outcome_follow_ups=outcome_follow_ups,
            outcome_deadline_at=outcome_deadline_at,
            start_state_revision=start_state_revision,
            business_outcome=business_outcome,
            disposition=disposition,
            latest_review=latest_review,
            reviews=reviews,
            reviews_truncated=reviews_truncated,
            replaces_progress_ids=replaces_progress_ids,
            applicability=applicability,
        )

        return operation_workflow_playbook_progress
