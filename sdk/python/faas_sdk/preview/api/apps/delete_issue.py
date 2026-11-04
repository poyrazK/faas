from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    issue_id: UUID,
    *,
    abandon_unconfirmed_tickets: bool | Unset = False,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["abandon_unconfirmed_tickets"] = abandon_unconfirmed_tickets

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/issues/{issue_id}".format(
            slug=quote(str(slug), safe=""),
            issue_id=quote(str(issue_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

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

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

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
    issue_id: UUID,
    *,
    client: AuthenticatedClient,
    abandon_unconfirmed_tickets: bool | Unset = False,
) -> Response[Any | Problem]:
    """Permanently delete a standalone issue and its retained history.

     Split linked groups and remove derived branches first. A later occurrence may create a new issue.

    Args:
        slug (str):
        issue_id (UUID):
        abandon_unconfirmed_tickets (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        issue_id=issue_id,
        abandon_unconfirmed_tickets=abandon_unconfirmed_tickets,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    issue_id: UUID,
    *,
    client: AuthenticatedClient,
    abandon_unconfirmed_tickets: bool | Unset = False,
) -> Any | Problem | None:
    """Permanently delete a standalone issue and its retained history.

     Split linked groups and remove derived branches first. A later occurrence may create a new issue.

    Args:
        slug (str):
        issue_id (UUID):
        abandon_unconfirmed_tickets (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        issue_id=issue_id,
        client=client,
        abandon_unconfirmed_tickets=abandon_unconfirmed_tickets,
    ).parsed


async def asyncio_detailed(
    slug: str,
    issue_id: UUID,
    *,
    client: AuthenticatedClient,
    abandon_unconfirmed_tickets: bool | Unset = False,
) -> Response[Any | Problem]:
    """Permanently delete a standalone issue and its retained history.

     Split linked groups and remove derived branches first. A later occurrence may create a new issue.

    Args:
        slug (str):
        issue_id (UUID):
        abandon_unconfirmed_tickets (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        issue_id=issue_id,
        abandon_unconfirmed_tickets=abandon_unconfirmed_tickets,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    issue_id: UUID,
    *,
    client: AuthenticatedClient,
    abandon_unconfirmed_tickets: bool | Unset = False,
) -> Any | Problem | None:
    """Permanently delete a standalone issue and its retained history.

     Split linked groups and remove derived branches first. A later occurrence may create a new issue.

    Args:
        slug (str):
        issue_id (UUID):
        abandon_unconfirmed_tickets (bool | Unset):  Default: False.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            issue_id=issue_id,
            client=client,
            abandon_unconfirmed_tickets=abandon_unconfirmed_tickets,
        )
    ).parsed
