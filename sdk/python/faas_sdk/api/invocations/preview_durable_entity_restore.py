from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.durable_entity_restore_preview import DurableEntityRestorePreview
from ...models.durable_entity_restore_request import DurableEntityRestoreRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: DurableEntityRestoreRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/entities/restore/preview".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DurableEntityRestorePreview | Problem | None:
    if response.status_code == 200:
        response_200 = DurableEntityRestorePreview.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DurableEntityRestorePreview | Problem]:
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
    body: DurableEntityRestoreRequest,
) -> Response[DurableEntityRestorePreview | Problem]:
    """Preview a restore without writes or execution.

     Owner diagnostic preview requiring apps:read or admin and MFA where
    applicable. Requires durable entity app enablement. Private, no-store.
    No ownership acquisition, guest execution or writes. Backup listing is
    bounded and metadata-only; backup reads contain sensitive application data.
    Restore preview reports observed versions, recognized schema envelopes and
    preserved pending work. Compatibility is always unverified; schema equality
    does not validate application data. Preview grants no restore authority.

    Args:
        slug (str):
        body (DurableEntityRestoreRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityRestorePreview | Problem]
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
    body: DurableEntityRestoreRequest,
) -> DurableEntityRestorePreview | Problem | None:
    """Preview a restore without writes or execution.

     Owner diagnostic preview requiring apps:read or admin and MFA where
    applicable. Requires durable entity app enablement. Private, no-store.
    No ownership acquisition, guest execution or writes. Backup listing is
    bounded and metadata-only; backup reads contain sensitive application data.
    Restore preview reports observed versions, recognized schema envelopes and
    preserved pending work. Compatibility is always unverified; schema equality
    does not validate application data. Preview grants no restore authority.

    Args:
        slug (str):
        body (DurableEntityRestoreRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityRestorePreview | Problem
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
    body: DurableEntityRestoreRequest,
) -> Response[DurableEntityRestorePreview | Problem]:
    """Preview a restore without writes or execution.

     Owner diagnostic preview requiring apps:read or admin and MFA where
    applicable. Requires durable entity app enablement. Private, no-store.
    No ownership acquisition, guest execution or writes. Backup listing is
    bounded and metadata-only; backup reads contain sensitive application data.
    Restore preview reports observed versions, recognized schema envelopes and
    preserved pending work. Compatibility is always unverified; schema equality
    does not validate application data. Preview grants no restore authority.

    Args:
        slug (str):
        body (DurableEntityRestoreRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DurableEntityRestorePreview | Problem]
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
    body: DurableEntityRestoreRequest,
) -> DurableEntityRestorePreview | Problem | None:
    """Preview a restore without writes or execution.

     Owner diagnostic preview requiring apps:read or admin and MFA where
    applicable. Requires durable entity app enablement. Private, no-store.
    No ownership acquisition, guest execution or writes. Backup listing is
    bounded and metadata-only; backup reads contain sensitive application data.
    Restore preview reports observed versions, recognized schema envelopes and
    preserved pending work. Compatibility is always unverified; schema equality
    does not validate application data. Preview grants no restore authority.

    Args:
        slug (str):
        body (DurableEntityRestoreRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DurableEntityRestorePreview | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
