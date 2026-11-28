from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.record_release_outcome_check_request import RecordReleaseOutcomeCheckRequest
from ...models.release_outcome_check import ReleaseOutcomeCheck
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: RecordReleaseOutcomeCheckRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/deployments/{id}/release-outcome".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ReleaseOutcomeCheck | None:
    if response.status_code == 200:
        response_200 = ReleaseOutcomeCheck.from_dict(response.json())

        return response_200

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ReleaseOutcomeCheck]:
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
    body: RecordReleaseOutcomeCheckRequest,
) -> Response[Problem | ReleaseOutcomeCheck]:
    """Record a database check after a release command with an unknown outcome.

     A release command that started but ended without an exit code (its worker was lost, it timed out or
    it was cancelled while running) may have changed the database partly. Until a person checks the
    database and records it here, or a later release command of the same scope succeeds, the app's next
    release fails with `release_outcome_uncertain` before its own command runs. 409 when this release's
    command never ran, ended with a known exit, or was checked already.

    Args:
        id (str):
        body (RecordReleaseOutcomeCheckRequest): What a person checked in the database after a
            release command with an unknown outcome.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReleaseOutcomeCheck]
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
    body: RecordReleaseOutcomeCheckRequest,
) -> Problem | ReleaseOutcomeCheck | None:
    """Record a database check after a release command with an unknown outcome.

     A release command that started but ended without an exit code (its worker was lost, it timed out or
    it was cancelled while running) may have changed the database partly. Until a person checks the
    database and records it here, or a later release command of the same scope succeeds, the app's next
    release fails with `release_outcome_uncertain` before its own command runs. 409 when this release's
    command never ran, ended with a known exit, or was checked already.

    Args:
        id (str):
        body (RecordReleaseOutcomeCheckRequest): What a person checked in the database after a
            release command with an unknown outcome.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReleaseOutcomeCheck
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
    body: RecordReleaseOutcomeCheckRequest,
) -> Response[Problem | ReleaseOutcomeCheck]:
    """Record a database check after a release command with an unknown outcome.

     A release command that started but ended without an exit code (its worker was lost, it timed out or
    it was cancelled while running) may have changed the database partly. Until a person checks the
    database and records it here, or a later release command of the same scope succeeds, the app's next
    release fails with `release_outcome_uncertain` before its own command runs. 409 when this release's
    command never ran, ended with a known exit, or was checked already.

    Args:
        id (str):
        body (RecordReleaseOutcomeCheckRequest): What a person checked in the database after a
            release command with an unknown outcome.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ReleaseOutcomeCheck]
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
    body: RecordReleaseOutcomeCheckRequest,
) -> Problem | ReleaseOutcomeCheck | None:
    """Record a database check after a release command with an unknown outcome.

     A release command that started but ended without an exit code (its worker was lost, it timed out or
    it was cancelled while running) may have changed the database partly. Until a person checks the
    database and records it here, or a later release command of the same scope succeeds, the app's next
    release fails with `release_outcome_uncertain` before its own command runs. 409 when this release's
    command never ran, ended with a known exit, or was checked already.

    Args:
        id (str):
        body (RecordReleaseOutcomeCheckRequest): What a person checked in the database after a
            release command with an unknown outcome.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ReleaseOutcomeCheck
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
