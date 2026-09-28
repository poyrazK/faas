export async function deliverWithRetry(send, url, body, maxAttempts = 2) {
  const statuses = [];
  for (let attempt = 0; attempt < maxAttempts; attempt++) {
    const response = await send(url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body,
    });
    statuses.push(response.status);
    if (response.ok) return statuses;
  }
  throw new Error(`delivery failed after ${maxAttempts} attempts: ${statuses.join(",")}`);
}
