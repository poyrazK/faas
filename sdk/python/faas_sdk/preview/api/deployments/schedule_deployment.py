from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.schedule_release_request import ScheduleReleaseRequest
from ...models.scheduled_release import ScheduledRelease
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: ScheduleReleaseRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/deployments/{id}/schedule".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ScheduledRelease | None:
    if response.status_code == 200:
        response_200 = ScheduledRelease.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ScheduledRelease]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ScheduleReleaseRequest,
) -> Response[Problem | ScheduledRelease]:
    """Schedule, or move, the start of a release waiting without traffic.

     The release must be live without traffic and without a canary ladder, or still building to go live
    without traffic; otherwise 409 `release_not_parked`. A release_at that has passed starts it on the
    API's next pass. Freezes active at release_at refuse the schedule with 409 `release_frozen` unless
    override_freeze gives a reason. An app that requires approvals needs a canary: 409
    `release_approval_canary_required`. An app's earlier pending schedule is cancelled.

    Args:
        id (str):
        body (ScheduleReleaseRequest): When and how a release waiting without traffic starts its
            rollout.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScheduledRelease]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ScheduleReleaseRequest,
) -> Problem | ScheduledRelease | None:
    """Schedule, or move, the start of a release waiting without traffic.

     The release must be live without traffic and without a canary ladder, or still building to go live
    without traffic; otherwise 409 `release_not_parked`. A release_at that has passed starts it on the
    API's next pass. Freezes active at release_at refuse the schedule with 409 `release_frozen` unless
    override_freeze gives a reason. An app that requires approvals needs a canary: 409
    `release_approval_canary_required`. An app's earlier pending schedule is cancelled.

    Args:
        id (str):
        body (ScheduleReleaseRequest): When and how a release waiting without traffic starts its
            rollout.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScheduledRelease
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ScheduleReleaseRequest,
) -> Response[Problem | ScheduledRelease]:
    """Schedule, or move, the start of a release waiting without traffic.

     The release must be live without traffic and without a canary ladder, or still building to go live
    without traffic; otherwise 409 `release_not_parked`. A release_at that has passed starts it on the
    API's next pass. Freezes active at release_at refuse the schedule with 409 `release_frozen` unless
    override_freeze gives a reason. An app that requires approvals needs a canary: 409
    `release_approval_canary_required`. An app's earlier pending schedule is cancelled.

    Args:
        id (str):
        body (ScheduleReleaseRequest): When and how a release waiting without traffic starts its
            rollout.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ScheduledRelease]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: ScheduleReleaseRequest,
) -> Problem | ScheduledRelease | None:
    """Schedule, or move, the start of a release waiting without traffic.

     The release must be live without traffic and without a canary ladder, or still building to go live
    without traffic; otherwise 409 `release_not_parked`. A release_at that has passed starts it on the
    API's next pass. Freezes active at release_at refuse the schedule with 409 `release_frozen` unless
    override_freeze gives a reason. An app that requires approvals needs a canary: 409
    `release_approval_canary_required`. An app's earlier pending schedule is cancelled.

    Args:
        id (str):
        body (ScheduleReleaseRequest): When and how a release waiting without traffic starts its
            rollout.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ScheduledRelease
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
