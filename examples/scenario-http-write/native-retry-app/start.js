import { createNativeRetryCaller } from "./server.js";
try {
  if (process.env.GREGALE_RETRY_BUDGET_FIXTURE !== "1") throw new Error("fixture_opt_in_required");
  const fixture = createNativeRetryCaller({ dependencyURL: process.env.PAYMENT_URL || "http://payment.svc.gregale:10080",
    qualificationMode: process.env.GREGALE_REQUEST_ATTEMPTS_MODE || "bounded", namespace: process.env.LEDGER_NAMESPACE, token: process.env.NATIVE_AUTH_CONTROL_TOKEN });
  fixture.server.listen(Number(process.env.PORT || 8080), process.env.HOST || "0.0.0.0");
} catch { console.error("Retry budget fixture startup failed"); process.exitCode = 1; }
