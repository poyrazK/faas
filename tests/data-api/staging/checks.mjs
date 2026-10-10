export class CanaryFailure extends Error {
  constructor(code) { super(code); this.code = code }
}
export const requireValue = (condition, code) => { if (!condition) throw new CanaryFailure(code) }
