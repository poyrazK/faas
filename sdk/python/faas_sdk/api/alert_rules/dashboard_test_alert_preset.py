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
    name: str,
    *,
    body: Any | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/dashboard/apps/{slug}/alert-presets/{name}/test".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 302:
        response_302 = cast(Any, None)
        return response_302

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

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


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: Any | Unset = UNSET,
) -> Response[Any | Problem]:
    r"""Form-POST sibling of testAlertPreset for the dashboard.

     Receives the per-card \"Send test alert\" form submission
    from the preset grid on /dashboard/apps/{slug}. No body
    fields — the instantiated rule already carries the
    webhook URL + secret. On success 302-redirects to
    /apps/{slug}?test_alert=ok; on 4xx/5xx 302-redirects to
    /apps/{slug}?test_alert=error so the template's flash
    banner can render. Web-cookie auth is sufficient — no
    MFA challenge.

    Args:
        slug (str):
        name (str):
        body (Any | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: Any | Unset = UNSET,
) -> Any | Problem | None:
    r"""Form-POST sibling of testAlertPreset for the dashboard.

     Receives the per-card \"Send test alert\" form submission
    from the preset grid on /dashboard/apps/{slug}. No body
    fields — the instantiated rule already carries the
    webhook URL + secret. On success 302-redirects to
    /apps/{slug}?test_alert=ok; on 4xx/5xx 302-redirects to
    /apps/{slug}?test_alert=error so the template's flash
    banner can render. Web-cookie auth is sufficient — no
    MFA challenge.

    Args:
        slug (str):
        name (str):
        body (Any | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: Any | Unset = UNSET,
) -> Response[Any | Problem]:
    r"""Form-POST sibling of testAlertPreset for the dashboard.

     Receives the per-card \"Send test alert\" form submission
    from the preset grid on /dashboard/apps/{slug}. No body
    fields — the instantiated rule already carries the
    webhook URL + secret. On success 302-redirects to
    /apps/{slug}?test_alert=ok; on 4xx/5xx 302-redirects to
    /apps/{slug}?test_alert=error so the template's flash
    banner can render. Web-cookie auth is sufficient — no
    MFA challenge.

    Args:
        slug (str):
        name (str):
        body (Any | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: Any | Unset = UNSET,
) -> Any | Problem | None:
    r"""Form-POST sibling of testAlertPreset for the dashboard.

     Receives the per-card \"Send test alert\" form submission
    from the preset grid on /dashboard/apps/{slug}. No body
    fields — the instantiated rule already carries the
    webhook URL + secret. On success 302-redirects to
    /apps/{slug}?test_alert=ok; on 4xx/5xx 302-redirects to
    /apps/{slug}?test_alert=error so the template's flash
    banner can render. Web-cookie auth is sufficient — no
    MFA challenge.

    Args:
        slug (str):
        name (str):
        body (Any | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
        )
    ).parsed
