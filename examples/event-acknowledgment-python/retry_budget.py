"""Application-owned budget staging and an explicitly persisted resume command."""

from dataclasses import dataclass

from faas_sdk import (
    EventAcknowledgmentIntentResumeRetryBudgetOptions,
    EventAcknowledgmentIntentRetryBudget,
    EventAcknowledgmentIntentRetryPolicy,
    aget_event_acknowledgment_intent_retry_resume_candidate,
    aresume_event_acknowledgment_intent_retry_budget,
)


async def stage_with_retry_budget(worker, connection, apply_business_sql, shutdown):
    # Authenticate delivery and retain exclusive ingestion-job ownership first.
    # The callback uses only its supplied cursor and returns exact JSON bytes/text.
    return await worker.run_durable(
        connection,
        apply_business_sql,
        stop_event=shutdown,
        retry_budget=EventAcknowledgmentIntentRetryBudget(
            8, EventAcknowledgmentIntentRetryPolicy(1000, 300000)
        ),
    )


@dataclass(frozen=True, slots=True)
class SavedResume:
    invocation_id: str
    replay_generation: int
    attempt: int
    options: EventAcknowledgmentIntentResumeRetryBudgetOptions


async def select_retry_resume(connection, scope, key, command_id, new_total, shutdown):
    # Read exact metadata for a deliberate, authorized operator decision.
    selected = await aget_event_acknowledgment_intent_retry_resume_candidate(
        connection,
        scope.account_id,
        scope.app_id,
        key.invocation_id,
        key.replay_generation,
        key.attempt,
        stop_event=shutdown,
    )
    if (
        selected.state != "parked"
        or selected.retry_policy is None
        or selected.retry_policy.version != 2
    ):
        raise ValueError("select a parked budgeted intent")
    return SavedResume(
        selected.invocation_id,
        selected.replay_generation,
        selected.attempt,
        EventAcknowledgmentIntentResumeRetryBudgetOptions(
            command_id,
            selected.updated_at,
            selected.delivery_attempts,
            selected.delivery_attempt_limit,
            new_total,
        ),
    )


async def apply_retry_resume(connection, scope, saved_command, shutdown):
    # Durably persist the command and trusted owner scope BEFORE calling.
    # After uncertainty, reuse every original field on a fresh connection.
    return await aresume_event_acknowledgment_intent_retry_budget(
        connection,
        scope.account_id,
        scope.app_id,
        saved_command.invocation_id,
        saved_command.replay_generation,
        saved_command.attempt,
        saved_command.options,
        stop_event=shutdown,
    )
