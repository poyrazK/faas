from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.data_api_change_hook_report import DataAPIChangeHookReport
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: DataAPIChangeHookReport,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/runtime/data-api/change-status",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
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

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

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
    *,
    client: AuthenticatedClient,
    body: DataAPIChangeHookReport,
) -> Response[Any | Problem]:
    """Report Data API change hook delivery

     Private runtime surface for change hook delivery. Requires the same gregale:data-api:events workload
    assertion from a live instance of an active app as publication; account and app come from it, never
    from the body. The report carries counts the runtime read from its change event table (waiting and
    rejected changes, when the oldest waiting change was recorded, the latest rejection code, whether
    publications keep failing) and when it last published; it never carries change data. Owners read the
    latest report in the app's Data API status. Owner API keys and application JWTs are not accepted.

    Args:
        body (DataAPIChangeHookReport): A runtime's change hook delivery report, read from its
            change event table. oldest_pending_at is present exactly when changes are pending.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    body: DataAPIChangeHookReport,
) -> Any | Problem | None:
    """Report Data API change hook delivery

     Private runtime surface for change hook delivery. Requires the same gregale:data-api:events workload
    assertion from a live instance of an active app as publication; account and app come from it, never
    from the body. The report carries counts the runtime read from its change event table (waiting and
    rejected changes, when the oldest waiting change was recorded, the latest rejection code, whether
    publications keep failing) and when it last published; it never carries change data. Owners read the
    latest report in the app's Data API status. Owner API keys and application JWTs are not accepted.

    Args:
        body (DataAPIChangeHookReport): A runtime's change hook delivery report, read from its
            change event table. oldest_pending_at is present exactly when changes are pending.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: DataAPIChangeHookReport,
) -> Response[Any | Problem]:
    """Report Data API change hook delivery

     Private runtime surface for change hook delivery. Requires the same gregale:data-api:events workload
    assertion from a live instance of an active app as publication; account and app come from it, never
    from the body. The report carries counts the runtime read from its change event table (waiting and
    rejected changes, when the oldest waiting change was recorded, the latest rejection code, whether
    publications keep failing) and when it last published; it never carries change data. Owners read the
    latest report in the app's Data API status. Owner API keys and application JWTs are not accepted.

    Args:
        body (DataAPIChangeHookReport): A runtime's change hook delivery report, read from its
            change event table. oldest_pending_at is present exactly when changes are pending.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: DataAPIChangeHookReport,
) -> Any | Problem | None:
    """Report Data API change hook delivery

     Private runtime surface for change hook delivery. Requires the same gregale:data-api:events workload
    assertion from a live instance of an active app as publication; account and app come from it, never
    from the body. The report carries counts the runtime read from its change event table (waiting and
    rejected changes, when the oldest waiting change was recorded, the latest rejection code, whether
    publications keep failing) and when it last published; it never carries change data. Owners read the
    latest report in the app's Data API status. Owner API keys and application JWTs are not accepted.

    Args:
        body (DataAPIChangeHookReport): A runtime's change hook delivery report, read from its
            change event table. oldest_pending_at is present exactly when changes are pending.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
