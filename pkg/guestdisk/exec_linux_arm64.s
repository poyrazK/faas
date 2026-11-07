#include "textflag.h"

// Linux prlimit64(0, RLIMIT_AS, limit, nil), then execve(path, argv, envv).
// No calls (including compiler-inserted instrumentation) are safe in between.
TEXT ·execWithAddressLimit(SB), NOSPLIT, $0-40
	MOVD $261, R8 // SYS_PRLIMIT64
	MOVD ZR, R0
	MOVD $9, R1 // RLIMIT_AS
	MOVD limit+24(FP), R2
	MOVD ZR, R3
	SVC $0
	CBNZ R0, failed
	MOVD path+0(FP), R0
	MOVD argv+8(FP), R1
	MOVD envv+16(FP), R2
	MOVD $221, R8 // SYS_EXECVE
	SVC $0
failed:
	NEG R0, R0
	MOVD R0, ret+32(FP)
	RET
