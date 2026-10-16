from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.issue_saved_view import IssueSavedView
from ...models.problem import Problem
from ...models.put_issue_saved_view_request import PutIssueSavedViewRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    view_key: str,
    *,
    body: PutIssueSavedViewRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/issue-saved-views/{view_key}".format(
            slug=quote(str(slug), safe=""),
            view_key=quote(str(view_key), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | IssueSavedView | Problem | None:
    if response.status_code == 200:
        response_200 = IssueSavedView.from_dict(response.json())

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

    if response.status_code == 409:
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | IssueSavedView | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    view_key: str,
    *,
    client: AuthenticatedClient,
    body: PutIssueSavedViewRequest,
) -> Response[Any | IssueSavedView | Problem]:
    """Save or replace a shared named issue inbox view.

     Requires deploy-write permission and session MFA. Existing keys replace their definition; new keys
    consume the 20-view app quota. Filters are bounded known fields, not executable queries. UUID
    assignees must be currently eligible app owners or active organization members. Views confer no
    additional read permission.

    Args:
        slug (str):
        view_key (str):
        body (PutIssueSavedViewRequest): Shared issue-view display name and validated replacement
            filter set.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | IssueSavedView | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        view_key=view_key,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    view_key: str,
    *,
    client: AuthenticatedClient,
    body: PutIssueSavedViewRequest,
) -> Any | IssueSavedView | Problem | None:
    """Save or replace a shared named issue inbox view.

     Requires deploy-write permission and session MFA. Existing keys replace their definition; new keys
    consume the 20-view app quota. Filters are bounded known fields, not executable queries. UUID
    assignees must be currently eligible app owners or active organization members. Views confer no
    additional read permission.

    Args:
        slug (str):
        view_key (str):
        body (PutIssueSavedViewRequest): Shared issue-view display name and validated replacement
            filter set.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | IssueSavedView | Problem
    """

    return sync_detailed(
        slug=slug,
        view_key=view_key,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    view_key: str,
    *,
    client: AuthenticatedClient,
    body: PutIssueSavedViewRequest,
) -> Response[Any | IssueSavedView | Problem]:
    """Save or replace a shared named issue inbox view.

     Requires deploy-write permission and session MFA. Existing keys replace their definition; new keys
    consume the 20-view app quota. Filters are bounded known fields, not executable queries. UUID
    assignees must be currently eligible app owners or active organization members. Views confer no
    additional read permission.

    Args:
        slug (str):
        view_key (str):
        body (PutIssueSavedViewRequest): Shared issue-view display name and validated replacement
            filter set.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | IssueSavedView | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        view_key=view_key,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    view_key: str,
    *,
    client: AuthenticatedClient,
    body: PutIssueSavedViewRequest,
) -> Any | IssueSavedView | Problem | None:
    """Save or replace a shared named issue inbox view.

     Requires deploy-write permission and session MFA. Existing keys replace their definition; new keys
    consume the 20-view app quota. Filters are bounded known fields, not executable queries. UUID
    assignees must be currently eligible app owners or active organization members. Views confer no
    additional read permission.

    Args:
        slug (str):
        view_key (str):
        body (PutIssueSavedViewRequest): Shared issue-view display name and validated replacement
            filter set.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | IssueSavedView | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            view_key=view_key,
            client=client,
            body=body,
        )
    ).parsed
