package state

// RetryStageState reports the actual executable starting point. Intermediate
// files are mutable, node-local outputs; they are not retained checkpoints for
// a new deployment ID. Rebuild prerequisites and rerun all security gates.
func RetryStageState(requested StageName) StageState {
	return StageState{
		Current:             StageSourceDownload,
		History:             []StageStateItem{},
		RetryRequestedStage: requested,
		RetryRestartReason:  "Intermediate stage checkpoints are not retained; rebuilding from the original source or image.",
	}
}
