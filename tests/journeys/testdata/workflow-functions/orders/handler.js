export default async function handler(input, wf) {
  const user = await wf.call("users", { email: input.email });
  const approval = await wf.waitForEvent("approval", { event: "approved" });
  return { user, approval };
}
