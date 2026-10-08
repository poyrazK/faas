from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.crash_snapshot_settings_request import CrashSnapshotSettingsRequest
from ...models.crash_snapshot_settings_response import CrashSnapshotSettingsResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CrashSnapshotSettingsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/crash-snapshots/settings".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CrashSnapshotSettingsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CrashSnapshotSettingsResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CrashSnapshotSettingsResponse | Problem]:
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
    body: CrashSnapshotSettingsRequest,
) -> Response[CrashSnapshotSettingsResponse | Problem]:
    """Turn 5xx crash snapshots on or off for an app.

     When on, the first 5xx answered by one of the app's instances
    captures that instance's memory (ADR-733), at most one in flight and
    none within 10 minutes of the last. Pro and Scale only. Answers 501
    `crash_snapshots_not_enabled` until the operator enables the feature.

    Args:
        slug (str):
        body (CrashSnapshotSettingsRequest): Body of `PUT /v1/apps/{slug}/crash-
            snapshots/settings`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CrashSnapshotSettingsResponse | Problem]
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
    body: CrashSnapshotSettingsRequest,
) -> CrashSnapshotSettingsResponse | Problem | None:
    """Turn 5xx crash snapshots on or off for an app.

     When on, the first 5xx answered by one of the app's instances
    captures that instance's memory (ADR-733), at most one in flight and
    none within 10 minutes of the last. Pro and Scale only. Answers 501
    `crash_snapshots_not_enabled` until the operator enables the feature.

    Args:
        slug (str):
        body (CrashSnapshotSettingsRequest): Body of `PUT /v1/apps/{slug}/crash-
            snapshots/settings`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CrashSnapshotSettingsResponse | Problem
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
    body: CrashSnapshotSettingsRequest,
) -> Response[CrashSnapshotSettingsResponse | Problem]:
    """Turn 5xx crash snapshots on or off for an app.

     When on, the first 5xx answered by one of the app's instances
    captures that instance's memory (ADR-733), at most one in flight and
    none within 10 minutes of the last. Pro and Scale only. Answers 501
    `crash_snapshots_not_enabled` until the operator enables the feature.

    Args:
        slug (str):
        body (CrashSnapshotSettingsRequest): Body of `PUT /v1/apps/{slug}/crash-
            snapshots/settings`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CrashSnapshotSettingsResponse | Problem]
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
    body: CrashSnapshotSettingsRequest,
) -> CrashSnapshotSettingsResponse | Problem | None:
    """Turn 5xx crash snapshots on or off for an app.

     When on, the first 5xx answered by one of the app's instances
    captures that instance's memory (ADR-733), at most one in flight and
    none within 10 minutes of the last. Pro and Scale only. Answers 501
    `crash_snapshots_not_enabled` until the operator enables the feature.

    Args:
        slug (str):
        body (CrashSnapshotSettingsRequest): Body of `PUT /v1/apps/{slug}/crash-
            snapshots/settings`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CrashSnapshotSettingsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
