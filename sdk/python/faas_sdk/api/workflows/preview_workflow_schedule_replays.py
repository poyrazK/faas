from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.workflow_schedule_replay_request import WorkflowScheduleReplayRequest
from ...models.workflow_schedule_replay_response import WorkflowScheduleReplayResponse
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: WorkflowScheduleReplayRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/workflows/schedules/occurrences:replay-preview".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | WorkflowScheduleReplayResponse | None:
    if response.status_code == 200:
        response_200 = WorkflowScheduleReplayResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | WorkflowScheduleReplayResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: WorkflowScheduleReplayRequest,
) -> Response[Problem | WorkflowScheduleReplayResponse]:
    """Preview selected skipped schedule occurrence replays

     Read-only advisory check of up to 20 retained skipped occurrences against the current live
    deployment, workflow definition, tenant schedule settings, overlap state, and app quota. Replay
    rechecks every condition.

    Args:
        slug (str):
        body (WorkflowScheduleReplayRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowScheduleReplayResponse]
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
    client: AuthenticatedClient | Client,
    body: WorkflowScheduleReplayRequest,
) -> Problem | WorkflowScheduleReplayResponse | None:
    """Preview selected skipped schedule occurrence replays

     Read-only advisory check of up to 20 retained skipped occurrences against the current live
    deployment, workflow definition, tenant schedule settings, overlap state, and app quota. Replay
    rechecks every condition.

    Args:
        slug (str):
        body (WorkflowScheduleReplayRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowScheduleReplayResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: WorkflowScheduleReplayRequest,
) -> Response[Problem | WorkflowScheduleReplayResponse]:
    """Preview selected skipped schedule occurrence replays

     Read-only advisory check of up to 20 retained skipped occurrences against the current live
    deployment, workflow definition, tenant schedule settings, overlap state, and app quota. Replay
    rechecks every condition.

    Args:
        slug (str):
        body (WorkflowScheduleReplayRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | WorkflowScheduleReplayResponse]
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
    client: AuthenticatedClient | Client,
    body: WorkflowScheduleReplayRequest,
) -> Problem | WorkflowScheduleReplayResponse | None:
    """Preview selected skipped schedule occurrence replays

     Read-only advisory check of up to 20 retained skipped occurrences against the current live
    deployment, workflow definition, tenant schedule settings, overlap state, and app quota. Replay
    rechecks every condition.

    Args:
        slug (str):
        body (WorkflowScheduleReplayRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | WorkflowScheduleReplayResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
