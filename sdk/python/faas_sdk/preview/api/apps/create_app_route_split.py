from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_route_split_request import CreateRouteSplitRequest
from ...models.problem import Problem
from ...models.route_split import RouteSplit
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateRouteSplitRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/route-splits".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | Problem | RouteSplit | None:
    if response.status_code == 202:
        response_202 = RouteSplit.from_dict(response.json())

        return response_202

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
        response_409 = cast(Any, None)
        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | Problem | RouteSplit]:
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
    body: CreateRouteSplitRequest,
) -> Response[Any | Problem | RouteSplit]:
    """Apply the suggested route split.

     Moves one heavy route to its own app. The body repeats the split the
    route-resources read suggested; Gregale recomputes it over the same
    window and answers route_split_changed when it moved. It then creates
    the route's app from the app's live build with its variables, secrets
    and runtime and security settings, and in the background routes the
    path to it with an edge rule once it is live and, for a memory split,
    sizes the app down. A split that fails before traffic moves deletes
    the route's app and ends rolled_back. Apps in a project, previews,
    apps with managed or service bindings, apps with their own edge rules
    and apps with a static egress IP are refused with
    route_split_unsupported. Available only where route resource peaks
    are enabled for the account.

    Args:
        slug (str):
        body (CreateRouteSplitRequest): The split the route-resources read suggested, repeated so
            a split that changed since is refused.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RouteSplit]
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
    body: CreateRouteSplitRequest,
) -> Any | Problem | RouteSplit | None:
    """Apply the suggested route split.

     Moves one heavy route to its own app. The body repeats the split the
    route-resources read suggested; Gregale recomputes it over the same
    window and answers route_split_changed when it moved. It then creates
    the route's app from the app's live build with its variables, secrets
    and runtime and security settings, and in the background routes the
    path to it with an edge rule once it is live and, for a memory split,
    sizes the app down. A split that fails before traffic moves deletes
    the route's app and ends rolled_back. Apps in a project, previews,
    apps with managed or service bindings, apps with their own edge rules
    and apps with a static egress IP are refused with
    route_split_unsupported. Available only where route resource peaks
    are enabled for the account.

    Args:
        slug (str):
        body (CreateRouteSplitRequest): The split the route-resources read suggested, repeated so
            a split that changed since is refused.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RouteSplit
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
    body: CreateRouteSplitRequest,
) -> Response[Any | Problem | RouteSplit]:
    """Apply the suggested route split.

     Moves one heavy route to its own app. The body repeats the split the
    route-resources read suggested; Gregale recomputes it over the same
    window and answers route_split_changed when it moved. It then creates
    the route's app from the app's live build with its variables, secrets
    and runtime and security settings, and in the background routes the
    path to it with an edge rule once it is live and, for a memory split,
    sizes the app down. A split that fails before traffic moves deletes
    the route's app and ends rolled_back. Apps in a project, previews,
    apps with managed or service bindings, apps with their own edge rules
    and apps with a static egress IP are refused with
    route_split_unsupported. Available only where route resource peaks
    are enabled for the account.

    Args:
        slug (str):
        body (CreateRouteSplitRequest): The split the route-resources read suggested, repeated so
            a split that changed since is refused.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem | RouteSplit]
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
    body: CreateRouteSplitRequest,
) -> Any | Problem | RouteSplit | None:
    """Apply the suggested route split.

     Moves one heavy route to its own app. The body repeats the split the
    route-resources read suggested; Gregale recomputes it over the same
    window and answers route_split_changed when it moved. It then creates
    the route's app from the app's live build with its variables, secrets
    and runtime and security settings, and in the background routes the
    path to it with an edge rule once it is live and, for a memory split,
    sizes the app down. A split that fails before traffic moves deletes
    the route's app and ends rolled_back. Apps in a project, previews,
    apps with managed or service bindings, apps with their own edge rules
    and apps with a static egress IP are refused with
    route_split_unsupported. Available only where route resource peaks
    are enabled for the account.

    Args:
        slug (str):
        body (CreateRouteSplitRequest): The split the route-resources read suggested, repeated so
            a split that changed since is refused.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem | RouteSplit
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
