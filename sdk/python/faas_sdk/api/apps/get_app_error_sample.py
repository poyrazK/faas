from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_error_sample_response import AppErrorSampleResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    fingerprint: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/errors/{fingerprint}/first".format(
            slug=quote(str(slug), safe=""),
            fingerprint=quote(str(fingerprint), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppErrorSampleResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppErrorSampleResponse.from_dict(response.json())

        return response_200

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
) -> Response[AppErrorSampleResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    fingerprint: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppErrorSampleResponse | Problem]:
    r"""Single oldest sample row + redacted headers (ADR-096 / PR-B).

     Returns the OLDEST request row for the fingerprint plus
    the redacted `headers_sample` (jsonb-decoded) and the
    list of `redactions_applied` pattern names so the
    dashboard can render a \"we redacted X / Y / Z\" badge.
    Returns 404 when the fingerprint has been purged.

    Args:
        slug (str):
        fingerprint (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppErrorSampleResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        fingerprint=fingerprint,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    fingerprint: str,
    *,
    client: AuthenticatedClient | Client,
) -> AppErrorSampleResponse | Problem | None:
    r"""Single oldest sample row + redacted headers (ADR-096 / PR-B).

     Returns the OLDEST request row for the fingerprint plus
    the redacted `headers_sample` (jsonb-decoded) and the
    list of `redactions_applied` pattern names so the
    dashboard can render a \"we redacted X / Y / Z\" badge.
    Returns 404 when the fingerprint has been purged.

    Args:
        slug (str):
        fingerprint (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppErrorSampleResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        fingerprint=fingerprint,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    fingerprint: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[AppErrorSampleResponse | Problem]:
    r"""Single oldest sample row + redacted headers (ADR-096 / PR-B).

     Returns the OLDEST request row for the fingerprint plus
    the redacted `headers_sample` (jsonb-decoded) and the
    list of `redactions_applied` pattern names so the
    dashboard can render a \"we redacted X / Y / Z\" badge.
    Returns 404 when the fingerprint has been purged.

    Args:
        slug (str):
        fingerprint (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppErrorSampleResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        fingerprint=fingerprint,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    fingerprint: str,
    *,
    client: AuthenticatedClient | Client,
) -> AppErrorSampleResponse | Problem | None:
    r"""Single oldest sample row + redacted headers (ADR-096 / PR-B).

     Returns the OLDEST request row for the fingerprint plus
    the redacted `headers_sample` (jsonb-decoded) and the
    list of `redactions_applied` pattern names so the
    dashboard can render a \"we redacted X / Y / Z\" badge.
    Returns 404 when the fingerprint has been purged.

    Args:
        slug (str):
        fingerprint (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppErrorSampleResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            fingerprint=fingerprint,
            client=client,
        )
    ).parsed
