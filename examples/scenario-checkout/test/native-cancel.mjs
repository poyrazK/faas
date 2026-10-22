// Called only by the native acceptance harness, after the charged outage
// assertions have passed. The parent interrupts Gregale, not this process.
const marker = process.env.GREGALE_NATIVE_CANCEL_MARKER;
if (!marker || !/^[a-f0-9]{32}$/.test(marker)) throw new Error("missing native cancellation marker");
console.log(marker);
await new Promise(resolve => setTimeout(resolve, 10 * 60 * 1000));
