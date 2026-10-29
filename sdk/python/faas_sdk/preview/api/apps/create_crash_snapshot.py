from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.crash_capture_response import CrashCaptureResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/crash-snapshots".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CrashCaptureResponse | Problem | None:
    if response.status_code == 202:
        response_202 = CrashCaptureResponse.from_dict(response.json())

        return response_202

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

    if response.status_code == 501:
        response_501 = Problem.from_dict(response.json())

        return response_501

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CrashCaptureResponse | Problem]:
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
    idempotency_key: str | Unset = UNSET,
) -> Response[CrashCaptureResponse | Problem]:
    """Capture the app's newest running instance now.

     Requests a crash snapshot of the app's newest running instance,
    whether or not 5xx captures are on. Refused with 409
    `crash_capture_refused` while another capture is in flight, within
    10 minutes of the last, or when no instance is running.

    Args:
        slug (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CrashCaptureResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> CrashCaptureResponse | Problem | None:
    """Capture the app's newest running instance now.

     Requests a crash snapshot of the app's newest running instance,
    whether or not 5xx captures are on. Refused with 409
    `crash_capture_refused` while another capture is in flight, within
    10 minutes of the last, or when no instance is running.

    Args:
        slug (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CrashCaptureResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> Response[CrashCaptureResponse | Problem]:
    """Capture the app's newest running instance now.

     Requests a crash snapshot of the app's newest running instance,
    whether or not 5xx captures are on. Refused with 409
    `crash_capture_refused` while another capture is in flight, within
    10 minutes of the last, or when no instance is running.

    Args:
        slug (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CrashCaptureResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient,
    idempotency_key: str | Unset = UNSET,
) -> CrashCaptureResponse | Problem | None:
    """Capture the app's newest running instance now.

     Requests a crash snapshot of the app's newest running instance,
    whether or not 5xx captures are on. Refused with 409
    `crash_capture_refused` while another capture is in flight, within
    10 minutes of the last, or when no instance is running.

    Args:
        slug (str):
        idempotency_key (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CrashCaptureResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            idempotency_key=idempotency_key,
        )
    ).parsed
