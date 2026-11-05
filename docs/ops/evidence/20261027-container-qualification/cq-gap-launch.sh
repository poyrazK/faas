#!/usr/bin/env bash
# Container qualification on current main plus the real-image smoke test:
# the containers lane, the image-run/job phase, then TestRealImageSmokeMetal.
# Same harness as /mnt/e2e-scratch/cq-20261024 (copied, not modified).
set -euo pipefail
R=/mnt/e2e-scratch/cq-gap-20261026
N="${1:?attempt}"
LANE='^(TestDirectOCIFullRootfsMetal|TestDirectOCIProcessContractMetal|TestDirectOCIMainHealthcheckMetal|TestDirectOCIMainHealthcheckWithCompanionMetal|TestDirectOCIPort3000Metal|TestDirectOCIAutoscaleScaleToZeroMetal|TestDeployHealthcheckMetal|TestAfterRestoreMetal|TestBeforeCheckpointMetal|TestTCPIngressMetal|TestUDPIngressMetal|TestE2E_Streaming_Metal_TTFBUnder1s|TestE2E_Streaming_Metal_TxBytesAccuracy|TestE2E_Streaming_Metal_PlanMatrix|TestE2E_Streaming_Metal_QuotaNonCounting|TestE2E_Streaming_Metal_H2CInnerLeg|TestSec11_MemoryMaxFenceEnforced_CrossProcess|TestSec11_SeccompFilterEnforced_CrossProcess|TestInteractiveAppTaskMetal)$'
JOBS='^(TestImageRunsMetal|TestImageRunProcessMetal|TestJobTaskLiveOutputMetal|TestInteractiveImageRunMetal)$'
SMOKE='^TestRealImageSmokeMetal$'
sudo env CQ_TESTS="$LANE;$JOBS;$SMOKE" CQ_SKIP_BUILD=0 CQ_QUIET_HOURS=18 FAAS_E2E_REAL_IMAGES=1 \
  setsid nohup bash "$R/cq2.sh" "$R" "$N" >/dev/null 2>&1 </dev/null &
echo "launched attempt $N"
