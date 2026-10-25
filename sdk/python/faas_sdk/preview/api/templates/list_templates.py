from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.template_view import TemplateView
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/templates",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | list[TemplateView] | None:
    if response.status_code == 200:
        response_200 = []
        _response_200 = response.json()
        for response_200_item_data in _response_200:
            response_200_item = TemplateView.from_dict(response_200_item_data)

            response_200.append(response_200_item)

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | list[TemplateView]]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | list[TemplateView]]:
    r"""Catalog of starter templates the dashboard wizard renders.

     Cookie-session-authenticated (NOT API-key). Mirrors the
    embed.FS in cmd/gregale/templates/ via cmd/gregale/templates.Names
    without importing the CLI's main package — the dashboard and
    the CLI read the same 13-entry list through independent paths.
    Adding a template means a new entry in cmd/gregale/templates/embed.go
    + the same category + description wiring here.

    Used by the dashboard's /dashboard/apps/new wizard to populate
    the \"Starting template\" dropdown. The CLI's `gregale deploy
    --template NAME` and `gregale init --template NAME` validators
    reference the same source on the CLI side.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[TemplateView]]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> Problem | list[TemplateView] | None:
    r"""Catalog of starter templates the dashboard wizard renders.

     Cookie-session-authenticated (NOT API-key). Mirrors the
    embed.FS in cmd/gregale/templates/ via cmd/gregale/templates.Names
    without importing the CLI's main package — the dashboard and
    the CLI read the same 13-entry list through independent paths.
    Adding a template means a new entry in cmd/gregale/templates/embed.go
    + the same category + description wiring here.

    Used by the dashboard's /dashboard/apps/new wizard to populate
    the \"Starting template\" dropdown. The CLI's `gregale deploy
    --template NAME` and `gregale init --template NAME` validators
    reference the same source on the CLI side.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[TemplateView]
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | list[TemplateView]]:
    r"""Catalog of starter templates the dashboard wizard renders.

     Cookie-session-authenticated (NOT API-key). Mirrors the
    embed.FS in cmd/gregale/templates/ via cmd/gregale/templates.Names
    without importing the CLI's main package — the dashboard and
    the CLI read the same 13-entry list through independent paths.
    Adding a template means a new entry in cmd/gregale/templates/embed.go
    + the same category + description wiring here.

    Used by the dashboard's /dashboard/apps/new wizard to populate
    the \"Starting template\" dropdown. The CLI's `gregale deploy
    --template NAME` and `gregale init --template NAME` validators
    reference the same source on the CLI side.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | list[TemplateView]]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> Problem | list[TemplateView] | None:
    r"""Catalog of starter templates the dashboard wizard renders.

     Cookie-session-authenticated (NOT API-key). Mirrors the
    embed.FS in cmd/gregale/templates/ via cmd/gregale/templates.Names
    without importing the CLI's main package — the dashboard and
    the CLI read the same 13-entry list through independent paths.
    Adding a template means a new entry in cmd/gregale/templates/embed.go
    + the same category + description wiring here.

    Used by the dashboard's /dashboard/apps/new wizard to populate
    the \"Starting template\" dropdown. The CLI's `gregale deploy
    --template NAME` and `gregale init --template NAME` validators
    reference the same source on the CLI side.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | list[TemplateView]
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
