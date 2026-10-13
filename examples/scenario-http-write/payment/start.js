import { createWriteProvider } from "./server.js";
createWriteProvider({ holdStage: process.env.HOLD_STAGE || "pending", holdFirstKey: process.env.HOLD_FIRST_KEY || "", ledgerPath: process.env.PAYMENT_LEDGER_PATH || "/tmp/http-write-charges.json" })
  .listen(Number(process.env.PORT || 8080), process.env.HOST || "0.0.0.0");
