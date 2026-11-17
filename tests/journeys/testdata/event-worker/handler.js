// Event subscriber for TestEventDelivery (tests/journeys).
//
// Zero dependencies. It logs one line per delivery carrying the event id, so
// the journey can see the envelope reached application code, and fails on
// purpose when the event's data says {"fail": true}, so the delivery
// exhausts its retries and lands in the dead-letter queue.

export async function handler(event) {
  let envelope = event?.body;
  if (typeof envelope === "string") {
    try {
      envelope = JSON.parse(envelope);
    } catch {
      envelope = null;
    }
  }
  if (!envelope || typeof envelope !== "object" || typeof envelope.id !== "string") {
    // Not an event delivery: a deploy smoke check or a manual invoke.
    return { statusCode: 200, headers: { "content-type": "application/json" }, body: JSON.stringify({ ok: true }) };
  }
  console.log(`e2e-event id=${envelope.id} source=${envelope.source} type=${envelope.type}`);
  if (envelope.data?.fail === true) {
    throw new Error(`e2e-event failing on purpose: ${envelope.id}`);
  }
  return {
    statusCode: 202,
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ accepted: true, id: envelope.id }),
  };
}
