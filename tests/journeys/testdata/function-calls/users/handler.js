// The callee. Its adapter checks typed-client calls against the input schema
// in gregale.yaml before this runs, so event.body always has an email here
// when the call came from a functions client.
let calls = 0;

export default async function handler(event) {
  calls++;
  const input = event.body || {};
  return { statusCode: 200, body: { probe: "gregale-e2e-users", hello: input.email, calls } };
}
