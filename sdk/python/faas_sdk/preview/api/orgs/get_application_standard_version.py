from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.application_standard_version import ApplicationStandardVersion
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    standard: str,
    *,
    version: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["version"] = version

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/orgs/{slug}/application-standards/{standard}".format(
            slug=quote(str(slug), safe=""),
            standard=quote(str(standard), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplicationStandardVersion | Problem | None:
    if response.status_code == 200:
        response_200 = ApplicationStandardVersion.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ApplicationStandardVersion | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    standard: str,
    *,
    client: AuthenticatedClient | Client,
    version: int | Unset = UNSET,
) -> Response[ApplicationStandardVersion | Problem]:
    """Read an immutable application standard version.

     Requires org.view_application_standards. Omitting version returns the latest published candidate.

    Args:
        slug (str):
        standard (str):
        version (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardVersion | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        standard=standard,
        version=version,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    standard: str,
    *,
    client: AuthenticatedClient | Client,
    version: int | Unset = UNSET,
) -> ApplicationStandardVersion | Problem | None:
    """Read an immutable application standard version.

     Requires org.view_application_standards. Omitting version returns the latest published candidate.

    Args:
        slug (str):
        standard (str):
        version (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardVersion | Problem
    """

    return sync_detailed(
        slug=slug,
        standard=standard,
        client=client,
        version=version,
    ).parsed


async def asyncio_detailed(
    slug: str,
    standard: str,
    *,
    client: AuthenticatedClient | Client,
    version: int | Unset = UNSET,
) -> Response[ApplicationStandardVersion | Problem]:
    """Read an immutable application standard version.

     Requires org.view_application_standards. Omitting version returns the latest published candidate.

    Args:
        slug (str):
        standard (str):
        version (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplicationStandardVersion | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        standard=standard,
        version=version,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    standard: str,
    *,
    client: AuthenticatedClient | Client,
    version: int | Unset = UNSET,
) -> ApplicationStandardVersion | Problem | None:
    """Read an immutable application standard version.

     Requires org.view_application_standards. Omitting version returns the latest published candidate.

    Args:
        slug (str):
        standard (str):
        version (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplicationStandardVersion | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            standard=standard,
            client=client,
            version=version,
        )
    ).parsed
