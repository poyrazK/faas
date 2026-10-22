const maxFrame = 1024 * 1024;

export function writeFrame(destination, frame) {
  if (destination.destroyed) return Promise.resolve();
  if (destination.write(frame)) return Promise.resolve();
  return new Promise(resolve => {
    const done = () => { destination.off("drain", done); destination.off("close", done); resolve(); };
    destination.once("drain", done); destination.once("close", done);
  });
}

// Pause while a frame is held, preserving order even for coalesced messages.
export function relayFrames(source, { startup = false, frame, invalid }) {
  let buffered = Buffer.alloc(0);
  let pumping = false;
  let pending = Promise.resolve();
  async function pump() {
    if (pumping) return;
    pumping = true;
    source.pause();
    try {
      while (!source.destroyed && buffered.length >= (startup ? 4 : 5)) {
        const size = startup ? buffered.readInt32BE(0) : 1 + buffered.readInt32BE(1);
        if (size < (startup ? 8 : 5) || size > maxFrame) { invalid(); return; }
        if (buffered.length < size) break;
        const message = buffered.subarray(0, size);
        buffered = buffered.subarray(size);
        const initial = startup;
        startup = false;
        await frame(message, initial);
      }
    } finally { pumping = false; if (!source.destroyed) source.resume(); }
  }
  source.on("data", chunk => {
    buffered = Buffer.concat([buffered, chunk]);
    if (buffered.length > maxFrame * 2) { invalid(); return; }
    if (!pumping) pending = pump().catch(invalid);
  });
  source.once("close", () => { buffered = Buffer.alloc(0); });
  return { join: () => pending };
}
