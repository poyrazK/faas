from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.throttle_suggestions_response import ThrottleSuggestionsResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    range_: str | Unset = "5m",
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["range"] = range_

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/throttle-suggestions".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ThrottleSuggestionsResponse | None:
    if response.status_code == 200:
        response_200 = ThrottleSuggestionsResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ThrottleSuggestionsResponse]:
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
    range_: str | Unset = "5m",
) -> Response[Problem | ThrottleSuggestionsResponse]:
    """Per-route throttle recommender (ADR-091 D20.5 amendment,
    issue #881). Read-only: returns a suggested rps/burst per
    route over the window, clamped to the customer's plan
    ceiling so the suggestion is always settable.

    The recommender is ADVICE-ONLY — it never auto-applies.
    Customers confirm via POST /v1/apps/{slug}/edge-rules.

    Args:
        slug (str):
        range_ (str | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ThrottleSuggestionsResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        range_=range_,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    range_: str | Unset = "5m",
) -> Problem | ThrottleSuggestionsResponse | None:
    """Per-route throttle recommender (ADR-091 D20.5 amendment,
    issue #881). Read-only: returns a suggested rps/burst per
    route over the window, clamped to the customer's plan
    ceiling so the suggestion is always settable.

    The recommender is ADVICE-ONLY — it never auto-applies.
    Customers confirm via POST /v1/apps/{slug}/edge-rules.

    Args:
        slug (str):
        range_ (str | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ThrottleSuggestionsResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        range_=range_,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    range_: str | Unset = "5m",
) -> Response[Problem | ThrottleSuggestionsResponse]:
    """Per-route throttle recommender (ADR-091 D20.5 amendment,
    issue #881). Read-only: returns a suggested rps/burst per
    route over the window, clamped to the customer's plan
    ceiling so the suggestion is always settable.

    The recommender is ADVICE-ONLY — it never auto-applies.
    Customers confirm via POST /v1/apps/{slug}/edge-rules.

    Args:
        slug (str):
        range_ (str | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ThrottleSuggestionsResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        range_=range_,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    range_: str | Unset = "5m",
) -> Problem | ThrottleSuggestionsResponse | None:
    """Per-route throttle recommender (ADR-091 D20.5 amendment,
    issue #881). Read-only: returns a suggested rps/burst per
    route over the window, clamped to the customer's plan
    ceiling so the suggestion is always settable.

    The recommender is ADVICE-ONLY — it never auto-applies.
    Customers confirm via POST /v1/apps/{slug}/edge-rules.

    Args:
        slug (str):
        range_ (str | Unset):  Default: '5m'.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ThrottleSuggestionsResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            range_=range_,
        )
    ).parsed
