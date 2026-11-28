from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.dev_sync_history_item import DevSyncHistoryItem
from ...models.problem import Problem
from ...models.record_dev_sync_request import RecordDevSyncRequest
from ...types import Response


def _get_kwargs(
    project: str,
    *,
    body: RecordDevSyncRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/dev/sessions/{project}/syncs".format(
            project=quote(str(project), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DevSyncHistoryItem | Problem | None:
    if response.status_code == 201:
        response_201 = DevSyncHistoryItem.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DevSyncHistoryItem | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    project: str,
    *,
    client: AuthenticatedClient | Client,
    body: RecordDevSyncRequest,
) -> Response[DevSyncHistoryItem | Problem]:
    """Record a redacted edit-to-live receipt.

     Stores bounded phase timings for one developer deployment. Repeating the same app and deployment ID
    is idempotent.

    Args:
        project (str):
        body (RecordDevSyncRequest): Redacted edit-to-live receipt written by the developer CLI.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DevSyncHistoryItem | Problem]
    """

    kwargs = _get_kwargs(
        project=project,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    project: str,
    *,
    client: AuthenticatedClient | Client,
    body: RecordDevSyncRequest,
) -> DevSyncHistoryItem | Problem | None:
    """Record a redacted edit-to-live receipt.

     Stores bounded phase timings for one developer deployment. Repeating the same app and deployment ID
    is idempotent.

    Args:
        project (str):
        body (RecordDevSyncRequest): Redacted edit-to-live receipt written by the developer CLI.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DevSyncHistoryItem | Problem
    """

    return sync_detailed(
        project=project,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    project: str,
    *,
    client: AuthenticatedClient | Client,
    body: RecordDevSyncRequest,
) -> Response[DevSyncHistoryItem | Problem]:
    """Record a redacted edit-to-live receipt.

     Stores bounded phase timings for one developer deployment. Repeating the same app and deployment ID
    is idempotent.

    Args:
        project (str):
        body (RecordDevSyncRequest): Redacted edit-to-live receipt written by the developer CLI.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DevSyncHistoryItem | Problem]
    """

    kwargs = _get_kwargs(
        project=project,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    project: str,
    *,
    client: AuthenticatedClient | Client,
    body: RecordDevSyncRequest,
) -> DevSyncHistoryItem | Problem | None:
    """Record a redacted edit-to-live receipt.

     Stores bounded phase timings for one developer deployment. Repeating the same app and deployment ID
    is idempotent.

    Args:
        project (str):
        body (RecordDevSyncRequest): Redacted edit-to-live receipt written by the developer CLI.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DevSyncHistoryItem | Problem
    """

    return (
        await asyncio_detailed(
            project=project,
            client=client,
            body=body,
        )
    ).parsed
