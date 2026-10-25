from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_response import AppResponse
from ...models.problem import Problem
from ...models.restore_app_body import RestoreAppBody
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: RestoreAppBody | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/restore".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> AppResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppResponse.from_dict(response.json())

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
) -> Response[AppResponse | Problem]:
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
    body: RestoreAppBody | Unset = UNSET,
) -> Response[AppResponse | Problem]:
    """Restore an app during its deletion grace window.

     Bodyless legacy callers remain supported. An optional expected_app_id
    fences the selected identity on updated servers. For mixed-version
    safety, use the dedicated deleted-app ID route instead of relying on
    legacy body handling. This route has no idempotent replay receipt.

    Args:
        slug (str):
        body (RestoreAppBody | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppResponse | Problem]
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
    body: RestoreAppBody | Unset = UNSET,
) -> AppResponse | Problem | None:
    """Restore an app during its deletion grace window.

     Bodyless legacy callers remain supported. An optional expected_app_id
    fences the selected identity on updated servers. For mixed-version
    safety, use the dedicated deleted-app ID route instead of relying on
    legacy body handling. This route has no idempotent replay receipt.

    Args:
        slug (str):
        body (RestoreAppBody | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppResponse | Problem
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
    body: RestoreAppBody | Unset = UNSET,
) -> Response[AppResponse | Problem]:
    """Restore an app during its deletion grace window.

     Bodyless legacy callers remain supported. An optional expected_app_id
    fences the selected identity on updated servers. For mixed-version
    safety, use the dedicated deleted-app ID route instead of relying on
    legacy body handling. This route has no idempotent replay receipt.

    Args:
        slug (str):
        body (RestoreAppBody | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppResponse | Problem]
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
    body: RestoreAppBody | Unset = UNSET,
) -> AppResponse | Problem | None:
    """Restore an app during its deletion grace window.

     Bodyless legacy callers remain supported. An optional expected_app_id
    fences the selected identity on updated servers. For mixed-version
    safety, use the dedicated deleted-app ID route instead of relying on
    legacy body handling. This route has no idempotent replay receipt.

    Args:
        slug (str):
        body (RestoreAppBody | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
