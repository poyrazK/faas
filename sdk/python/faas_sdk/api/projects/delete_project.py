from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    detach_services: bool | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["detach_services"] = detach_services

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/projects/{slug}".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
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
    detach_services: bool | Unset = UNSET,
) -> Response[Any | Problem]:
    """Delete a project and detach its live workloads.

     The apps remain live. Their project_id is cleared by the database
    foreign-key action. While the project's services call each other by
    service name, deletion is refused with 409
    project_services_call_each_other, because detached services no longer
    resolve those names; pass detach_services=true once they do not.

    Args:
        slug (str):
        detach_services (bool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        detach_services=detach_services,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    detach_services: bool | Unset = UNSET,
) -> Any | Problem | None:
    """Delete a project and detach its live workloads.

     The apps remain live. Their project_id is cleared by the database
    foreign-key action. While the project's services call each other by
    service name, deletion is refused with 409
    project_services_call_each_other, because detached services no longer
    resolve those names; pass detach_services=true once they do not.

    Args:
        slug (str):
        detach_services (bool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        detach_services=detach_services,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    detach_services: bool | Unset = UNSET,
) -> Response[Any | Problem]:
    """Delete a project and detach its live workloads.

     The apps remain live. Their project_id is cleared by the database
    foreign-key action. While the project's services call each other by
    service name, deletion is refused with 409
    project_services_call_each_other, because detached services no longer
    resolve those names; pass detach_services=true once they do not.

    Args:
        slug (str):
        detach_services (bool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        detach_services=detach_services,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    detach_services: bool | Unset = UNSET,
) -> Any | Problem | None:
    """Delete a project and detach its live workloads.

     The apps remain live. Their project_id is cleared by the database
    foreign-key action. While the project's services call each other by
    service name, deletion is refused with 409
    project_services_call_each_other, because detached services no longer
    resolve those names; pass detach_services=true once they do not.

    Args:
        slug (str):
        detach_services (bool | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            detach_services=detach_services,
        )
    ).parsed
