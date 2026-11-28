from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_protection_response import AppProtectionResponse
from ...models.problem import Problem
from ...models.set_app_protection_request import SetAppProtectionRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: SetAppProtectionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/protection".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppProtectionResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppProtectionResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppProtectionResponse | Problem]:
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
    body: SetAppProtectionRequest,
) -> Response[AppProtectionResponse | Problem]:
    """Turn the app's production protection on or off

     on enrolls the app, including one created before protection was available, and sets up the
    protection objects after its next live production deploy. off deletes the protection objects the
    customer has not changed; changed objects, objects a kept alert rule or release gate still uses, and
    the account email channel stay.

    Args:
        slug (str):
        body (SetAppProtectionRequest): Turns production protection on or off. on creates the
            baseline; off deletes the baseline objects you have not changed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppProtectionResponse | Problem]
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
    body: SetAppProtectionRequest,
) -> AppProtectionResponse | Problem | None:
    """Turn the app's production protection on or off

     on enrolls the app, including one created before protection was available, and sets up the
    protection objects after its next live production deploy. off deletes the protection objects the
    customer has not changed; changed objects, objects a kept alert rule or release gate still uses, and
    the account email channel stay.

    Args:
        slug (str):
        body (SetAppProtectionRequest): Turns production protection on or off. on creates the
            baseline; off deletes the baseline objects you have not changed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppProtectionResponse | Problem
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
    body: SetAppProtectionRequest,
) -> Response[AppProtectionResponse | Problem]:
    """Turn the app's production protection on or off

     on enrolls the app, including one created before protection was available, and sets up the
    protection objects after its next live production deploy. off deletes the protection objects the
    customer has not changed; changed objects, objects a kept alert rule or release gate still uses, and
    the account email channel stay.

    Args:
        slug (str):
        body (SetAppProtectionRequest): Turns production protection on or off. on creates the
            baseline; off deletes the baseline objects you have not changed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppProtectionResponse | Problem]
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
    body: SetAppProtectionRequest,
) -> AppProtectionResponse | Problem | None:
    """Turn the app's production protection on or off

     on enrolls the app, including one created before protection was available, and sets up the
    protection objects after its next live production deploy. off deletes the protection objects the
    customer has not changed; changed objects, objects a kept alert rule or release gate still uses, and
    the account email channel stay.

    Args:
        slug (str):
        body (SetAppProtectionRequest): Turns production protection on or off. on creates the
            baseline; off deletes the baseline objects you have not changed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppProtectionResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
