from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_profile_capture_request import CreateProfileCaptureRequest
from ...models.problem import Problem
from ...models.profile_capture import ProfileCapture
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: CreateProfileCaptureRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/profiles/captures".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | ProfileCapture | None:
    if response.status_code == 202:
        response_202 = ProfileCapture.from_dict(response.json())

        return response_202

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
) -> Response[Problem | ProfileCapture]:
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
    body: CreateProfileCaptureRequest | Unset = UNSET,
) -> Response[Problem | ProfileCapture]:
    """Capture CPU and/or heap profiles from a running instance now.

     Queues a bounded capture on the requested or earliest-started RUNNING instance; parked apps are not
    woken. Poll the capture until status is ready or failed. One capture per app runs at a time; an
    account can start 60 per hour. Gregale also queues a heap capture automatically (trigger
    memory_pressure) when an instance runs low on memory, at most once per hour per app.

    Args:
        slug (str):
        body (CreateProfileCaptureRequest | Unset): On-demand capture request. Kinds defaults to
            [cpu]; duration_seconds to 10. Stacks is an instant snapshot of where every goroutine or
            thread is waiting, taken when the capture starts. Block and mutex are Go contention
            profiles (time goroutines spent blocked on channels or locks, or waiting for a mutex,
            during the window).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileCapture]
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
    body: CreateProfileCaptureRequest | Unset = UNSET,
) -> Problem | ProfileCapture | None:
    """Capture CPU and/or heap profiles from a running instance now.

     Queues a bounded capture on the requested or earliest-started RUNNING instance; parked apps are not
    woken. Poll the capture until status is ready or failed. One capture per app runs at a time; an
    account can start 60 per hour. Gregale also queues a heap capture automatically (trigger
    memory_pressure) when an instance runs low on memory, at most once per hour per app.

    Args:
        slug (str):
        body (CreateProfileCaptureRequest | Unset): On-demand capture request. Kinds defaults to
            [cpu]; duration_seconds to 10. Stacks is an instant snapshot of where every goroutine or
            thread is waiting, taken when the capture starts. Block and mutex are Go contention
            profiles (time goroutines spent blocked on channels or locks, or waiting for a mutex,
            during the window).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileCapture
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
    body: CreateProfileCaptureRequest | Unset = UNSET,
) -> Response[Problem | ProfileCapture]:
    """Capture CPU and/or heap profiles from a running instance now.

     Queues a bounded capture on the requested or earliest-started RUNNING instance; parked apps are not
    woken. Poll the capture until status is ready or failed. One capture per app runs at a time; an
    account can start 60 per hour. Gregale also queues a heap capture automatically (trigger
    memory_pressure) when an instance runs low on memory, at most once per hour per app.

    Args:
        slug (str):
        body (CreateProfileCaptureRequest | Unset): On-demand capture request. Kinds defaults to
            [cpu]; duration_seconds to 10. Stacks is an instant snapshot of where every goroutine or
            thread is waiting, taken when the capture starts. Block and mutex are Go contention
            profiles (time goroutines spent blocked on channels or locks, or waiting for a mutex,
            during the window).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ProfileCapture]
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
    body: CreateProfileCaptureRequest | Unset = UNSET,
) -> Problem | ProfileCapture | None:
    """Capture CPU and/or heap profiles from a running instance now.

     Queues a bounded capture on the requested or earliest-started RUNNING instance; parked apps are not
    woken. Poll the capture until status is ready or failed. One capture per app runs at a time; an
    account can start 60 per hour. Gregale also queues a heap capture automatically (trigger
    memory_pressure) when an instance runs low on memory, at most once per hour per app.

    Args:
        slug (str):
        body (CreateProfileCaptureRequest | Unset): On-demand capture request. Kinds defaults to
            [cpu]; duration_seconds to 10. Stacks is an instant snapshot of where every goroutine or
            thread is waiting, taken when the capture starts. Block and mutex are Go contention
            profiles (time goroutines spent blocked on channels or locks, or waiting for a mutex,
            during the window).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ProfileCapture
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
