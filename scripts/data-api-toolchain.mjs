// Release bundles require an exact compiler version, independently of the
// module's language target. Older modules pin the compiler with `go` alone.
export function pinnedGoToolchain(source) {
  const directive = source.match(/^[ \t]*toolchain[ \t]+([^\s/]+)/m)
  if (directive) {
    const version = directive[1].match(/^go(\d+\.\d+\.\d+)$/)?.[1]
    if (!version) throw new Error('The repository toolchain directive must pin an exact Go release')
    return version
  }
  const version = source.match(/^[ \t]*go[ \t]+(\d+\.\d+\.\d+)(?=[ \t]*(?:\/\/|$))/m)?.[1]
  if (!version) throw new Error('The repository must pin an exact Go release')
  return version
}
