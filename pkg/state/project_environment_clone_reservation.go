package state

func (m *MemStore) checkProjectEnvironmentCloneReservationLocked(clone ProjectEnvironmentClone) error {
	for _, op := range m.projectEnvironmentCloneOperations {
		if op.ProjectID != clone.ProjectID || op.TargetEnvironment != clone.TargetSlug || !cloneOperationReservesTarget(op.Status) {
			continue
		}
		return validateProjectEnvironmentCloneOwner(clone, op.ID, op.SourceEnvironment, op.Status, op.Revision)
	}
	if clone.CloneOperationID != "" || clone.CloneOperationRevision != 0 {
		return ErrConflict
	}
	return nil
}

func validateProjectEnvironmentCloneOwner(clone ProjectEnvironmentClone, id, source, status string, revision int64) error {
	if clone.CloneOperationID == "" || clone.CloneOperationID != id || clone.CloneOperationRevision != revision ||
		clone.SourceSlug != source || status != CloneOperationCopying || clone.ShareResources {
		return ErrConflict
	}
	return nil
}
