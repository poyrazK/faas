#include "textflag.h"

// Linux prlimit64(0, RLIMIT_AS, limit, nil), then execve(path, argv, envv).
// No calls (including compiler-inserted instrumentation) are safe in between.
TEXT ·execWithAddressLimit(SB), NOSPLIT, $0-40
	MOVQ $302, AX // SYS_PRLIMIT64
	XORQ DI, DI
	MOVQ $9, SI // RLIMIT_AS
	MOVQ limit+24(FP), DX
	XORQ R10, R10
	SYSCALL
	TESTQ AX, AX
	JNE failed
	MOVQ path+0(FP), DI
	MOVQ argv+8(FP), SI
	MOVQ envv+16(FP), DX
	MOVQ $59, AX // SYS_EXECVE
	SYSCALL
failed:
	NEGQ AX
	MOVQ AX, ret+32(FP)
	RET
