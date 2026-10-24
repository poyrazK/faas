from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.event_replay_backfill_job_response import EventReplayBackfillJobResponse
from ...models.problem import Problem
from ...models.workflow_event_replay_backfill_request import WorkflowEventReplayBackfillRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: WorkflowEventReplayBackfillRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/workflow-event-replays".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> EventReplayBackfillJobResponse | Problem | None:
    if response.status_code == 202:
        response_202 = EventReplayBackfillJobResponse.from_dict(response.json())

        return response_202

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[EventReplayBackfillJobResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: WorkflowEventReplayBackfillRequest,
) -> Response[EventReplayBackfillJobResponse | Problem]:
    """Create a durable historical workflow-start backfill.

     Creates a durable job for one currently eligible event-triggered workflow
    in the app's preferred live default deployment. The selected workflow
    definition and trigger are snapshotted at creation. The half-open range
    uses platform acceptance time with a fixed exclusive cutoff and a
    maximum 30-day range. Only retained events with a known immutable
    recipient snapshot, a settled delivered receipt, and no captured
    membership for this app workflow are considered. Legacy events with an
    unknown snapshot, events that already captured this workflow, unsettled
    receipts, filtered events, and any event with a durable workflow
    admission receipt are skipped. A durable `(outbox_id, workflow
    recipient_id)` receipt deduplicates run admission across backfill jobs,
    including after the linked run is pruned. Admission uses the definition
    pinned to the job and current account/app eligibility and run quotas.
    Quota and temporary target failures can be retried with the standard
    backfill retry operation. The scan handles at most 100 envelopes per
    page; at most three jobs may run per account, with one active job per
    workflow. Active jobs protect their range from normal settled-receipt
    pruning. `enqueued` means a workflow run was admitted; run completion is
    separate. Retained event history is not a complete archive.

    Args:
        slug (str):
        body (WorkflowEventReplayBackfillRequest): Current event-triggered workflow and
            acceptance-time range for a durable historical run-admission job.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReplayBackfillJobResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: WorkflowEventReplayBackfillRequest,
) -> EventReplayBackfillJobResponse | Problem | None:
    """Create a durable historical workflow-start backfill.

     Creates a durable job for one currently eligible event-triggered workflow
    in the app's preferred live default deployment. The selected workflow
    definition and trigger are snapshotted at creation. The half-open range
    uses platform acceptance time with a fixed exclusive cutoff and a
    maximum 30-day range. Only retained events with a known immutable
    recipient snapshot, a settled delivered receipt, and no captured
    membership for this app workflow are considered. Legacy events with an
    unknown snapshot, events that already captured this workflow, unsettled
    receipts, filtered events, and any event with a durable workflow
    admission receipt are skipped. A durable `(outbox_id, workflow
    recipient_id)` receipt deduplicates run admission across backfill jobs,
    including after the linked run is pruned. Admission uses the definition
    pinned to the job and current account/app eligibility and run quotas.
    Quota and temporary target failures can be retried with the standard
    backfill retry operation. The scan handles at most 100 envelopes per
    page; at most three jobs may run per account, with one active job per
    workflow. Active jobs protect their range from normal settled-receipt
    pruning. `enqueued` means a workflow run was admitted; run completion is
    separate. Retained event history is not a complete archive.

    Args:
        slug (str):
        body (WorkflowEventReplayBackfillRequest): Current event-triggered workflow and
            acceptance-time range for a durable historical run-admission job.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReplayBackfillJobResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: WorkflowEventReplayBackfillRequest,
) -> Response[EventReplayBackfillJobResponse | Problem]:
    """Create a durable historical workflow-start backfill.

     Creates a durable job for one currently eligible event-triggered workflow
    in the app's preferred live default deployment. The selected workflow
    definition and trigger are snapshotted at creation. The half-open range
    uses platform acceptance time with a fixed exclusive cutoff and a
    maximum 30-day range. Only retained events with a known immutable
    recipient snapshot, a settled delivered receipt, and no captured
    membership for this app workflow are considered. Legacy events with an
    unknown snapshot, events that already captured this workflow, unsettled
    receipts, filtered events, and any event with a durable workflow
    admission receipt are skipped. A durable `(outbox_id, workflow
    recipient_id)` receipt deduplicates run admission across backfill jobs,
    including after the linked run is pruned. Admission uses the definition
    pinned to the job and current account/app eligibility and run quotas.
    Quota and temporary target failures can be retried with the standard
    backfill retry operation. The scan handles at most 100 envelopes per
    page; at most three jobs may run per account, with one active job per
    workflow. Active jobs protect their range from normal settled-receipt
    pruning. `enqueued` means a workflow run was admitted; run completion is
    separate. Retained event history is not a complete archive.

    Args:
        slug (str):
        body (WorkflowEventReplayBackfillRequest): Current event-triggered workflow and
            acceptance-time range for a durable historical run-admission job.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[EventReplayBackfillJobResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    body: WorkflowEventReplayBackfillRequest,
) -> EventReplayBackfillJobResponse | Problem | None:
    """Create a durable historical workflow-start backfill.

     Creates a durable job for one currently eligible event-triggered workflow
    in the app's preferred live default deployment. The selected workflow
    definition and trigger are snapshotted at creation. The half-open range
    uses platform acceptance time with a fixed exclusive cutoff and a
    maximum 30-day range. Only retained events with a known immutable
    recipient snapshot, a settled delivered receipt, and no captured
    membership for this app workflow are considered. Legacy events with an
    unknown snapshot, events that already captured this workflow, unsettled
    receipts, filtered events, and any event with a durable workflow
    admission receipt are skipped. A durable `(outbox_id, workflow
    recipient_id)` receipt deduplicates run admission across backfill jobs,
    including after the linked run is pruned. Admission uses the definition
    pinned to the job and current account/app eligibility and run quotas.
    Quota and temporary target failures can be retried with the standard
    backfill retry operation. The scan handles at most 100 envelopes per
    page; at most three jobs may run per account, with one active job per
    workflow. Active jobs protect their range from normal settled-receipt
    pruning. `enqueued` means a workflow run was admitted; run completion is
    separate. Retained event history is not a complete archive.

    Args:
        slug (str):
        body (WorkflowEventReplayBackfillRequest): Current event-triggered workflow and
            acceptance-time range for a durable historical run-admission job.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        EventReplayBackfillJobResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
