"""Separate explicit budget selection/persistence from guarded execution."""

from dataclasses import dataclass, replace

from faas_sdk import (
    EventAcknowledgmentIntentAdoptRetryBudgetOptions,
    EventAcknowledgmentIntentRetryBudget,
    EventAcknowledgmentIntentRetryPolicy,
    aadopt_event_acknowledgment_intent_retry_budget,
    aget_event_acknowledgment_intent_budget_adoption_candidate,
)


@dataclass(frozen=True, slots=True)
class SavedBudgetAdoption:
    invocation_id: str
    replay_generation: int
    attempt: int
    options: EventAcknowledgmentIntentAdoptRetryBudgetOptions


async def select_budget_adoption(connection, scope, key, command_id, total, shutdown):
    # Select exact metadata for a deliberate, authorized operator decision.
    selected = await aget_event_acknowledgment_intent_budget_adoption_candidate(
        connection,
        scope.account_id,
        scope.app_id,
        key.invocation_id,
        key.replay_generation,
        key.attempt,
        stop_event=shutdown,
    )
    prior = selected.retry_policy
    if (
        selected.state not in ("pending", "unconfirmed")
        or prior is not None
        and prior.version != 1
    ):
        raise ValueError("select an active unbound or v1 intent")
    expected = None if prior is None else replace(prior)
    timing = (
        None
        if prior is None
        else EventAcknowledgmentIntentRetryPolicy(
            prior.initial_backoff_ms, prior.max_backoff_ms
        )
    )
    return SavedBudgetAdoption(
        selected.invocation_id,
        selected.replay_generation,
        selected.attempt,
        EventAcknowledgmentIntentAdoptRetryBudgetOptions(
            command_id=command_id,
            expected_updated_at=selected.updated_at,
            expected_delivery_attempts=selected.delivery_attempts,
            retry_budget=EventAcknowledgmentIntentRetryBudget(total, timing),
            expected_policy=expected,
        ),
    )


async def apply_budget_adoption(connection, scope, saved_command, shutdown):
    # Authorize and durably save the command and trusted owner scope BEFORE calling.
    # After uncertainty, retry every original field unchanged on a fresh connection.
    return await aadopt_event_acknowledgment_intent_retry_budget(
        connection,
        scope.account_id,
        scope.app_id,
        saved_command.invocation_id,
        saved_command.replay_generation,
        saved_command.attempt,
        saved_command.options,
        stop_event=shutdown,
    )
