from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.edge_rule_async_action_retry_policy import EdgeRuleAsyncActionRetryPolicy


T = TypeVar("T", bound="EdgeRuleAsyncAction")


@_attrs_define
class EdgeRuleAsyncAction:
    """Marks the matched route for durable asynchronous execution (ADR-215).
    on_success and on_failure optionally select app webhook subscription
    IDs that receive the terminal job.finished invocation outcome. An
    optional retry_policy overrides the app retry curve for invocations
    accepted by this route. max_age_seconds starts when Gregale accepts a
    request; values above the plan's invocation deadline are clamped.
    Omitted values preserve existing app and plan defaults.

    """

    on_success: UUID | Unset = UNSET
    """Optional app webhook subscription ID for successful invocations."""
    on_failure: UUID | Unset = UNSET
    """Optional app webhook subscription ID for failed or dead-lettered invocations."""
    retry_policy: EdgeRuleAsyncActionRetryPolicy | Unset = UNSET
    """Optional per-route retry override; attempts are capped by the account plan."""
    max_age_seconds: int | Unset = UNSET
    """Maximum lifetime from acceptance; 0 or omission inherits the account-plan default, and lower plan ceilings
    clamp this value."""

    def to_dict(self) -> dict[str, Any]:
        on_success: str | Unset = UNSET
        if not isinstance(self.on_success, Unset):
            on_success = str(self.on_success)

        on_failure: str | Unset = UNSET
        if not isinstance(self.on_failure, Unset):
            on_failure = str(self.on_failure)

        retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy.to_dict()

        max_age_seconds = self.max_age_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if on_success is not UNSET:
            field_dict["on_success"] = on_success
        if on_failure is not UNSET:
            field_dict["on_failure"] = on_failure
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy
        if max_age_seconds is not UNSET:
            field_dict["max_age_seconds"] = max_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_async_action_retry_policy import EdgeRuleAsyncActionRetryPolicy

        d = dict(src_dict)
        _on_success = d.pop("on_success", UNSET)
        on_success: UUID | Unset
        if isinstance(_on_success, Unset):
            on_success = UNSET
        else:
            on_success = UUID(_on_success)

        _on_failure = d.pop("on_failure", UNSET)
        on_failure: UUID | Unset
        if isinstance(_on_failure, Unset):
            on_failure = UNSET
        else:
            on_failure = UUID(_on_failure)

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: EdgeRuleAsyncActionRetryPolicy | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = EdgeRuleAsyncActionRetryPolicy.from_dict(_retry_policy)

        max_age_seconds = d.pop("max_age_seconds", UNSET)

        edge_rule_async_action = cls(
            on_success=on_success,
            on_failure=on_failure,
            retry_policy=retry_policy,
            max_age_seconds=max_age_seconds,
        )

        return edge_rule_async_action
