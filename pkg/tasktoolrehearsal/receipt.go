package tasktoolrehearsal

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// FixtureArchives pins the inputs to the offline verifier. The private
// executor separately pins the reviewed source trees.
type FixtureArchives struct {
	Web    string `json:"web"`
	Worker string `json:"worker"`
}

type CleanupObservation struct {
	WorkloadsRemaining int  `json:"workloads_remaining"`
	DatabasePresent    bool `json:"database_present"`
	LoginsRemaining    int  `json:"logins_remaining"`
	SecretsRemaining   int  `json:"secrets_remaining"`
	SourcesRemaining   int  `json:"sources_remaining"`
}

// RecoveryReceipt is the evidence consumed by the offline hosted Task
// verifier. Its contents alone do not certify a native host.
type RecoveryReceipt struct {
	Version           int                `json:"version"`
	ArchiveSHA256     FixtureArchives    `json:"archive_sha256"`
	RunID             string             `json:"run_id"`
	TaskID            string             `json:"task_id"`
	Initial           ReceiptClaim       `json:"initial"`
	StoppedInstanceID string             `json:"stopped_instance_id"`
	Recovered         ReceiptClaim       `json:"recovered"`
	Result            ReceiptResult      `json:"result"`
	Observer          ReceiptObservation `json:"observer"`
	Cleanup           CleanupObservation `json:"cleanup"`
}

type ReceiptClaim struct {
	TaskID       string `json:"task_id"`
	Status       string `json:"status"`
	Attempt      int    `json:"attempt"`
	DeploymentID string `json:"deployment_id"`
	InstanceID   string `json:"instance_id"`
}

type ReceiptResult struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Text   string `json:"text"`
}

type ReceiptObservation struct {
	Namespace           string `json:"namespace"`
	TaskID              string `json:"task_id"`
	Attempt             int    `json:"attempt"`
	TaskRows            int    `json:"task_rows"`
	CompletedResultRows int    `json:"completed_result_rows"`
}

func NewRecoveryReceipt(runID string, archives FixtureArchives, evidence SmokeEvidence, cleanup CleanupObservation) (RecoveryReceipt, error) {
	var receipt RecoveryReceipt
	if !validSmokeID(runID) || !validSmokeID(evidence.TaskID) || !validSmokeID(evidence.Initial.DeploymentID) ||
		!toolpublication.ValidID(archives.Web) || !toolpublication.ValidID(archives.Worker) {
		return receipt, errors.New("invalid hosted Task receipt identity")
	}
	scope := SmokeScope{Namespace: "rehearsal-" + runID, WorkerDeploymentID: evidence.Initial.DeploymentID}
	if !matchesClaim(scope, evidence.Initial, evidence.TaskID, 1) || !matchesClaim(scope, evidence.Recovered, evidence.TaskID, 2) ||
		evidence.Recovered.InstanceID == evidence.Initial.InstanceID ||
		evidence.Stopped != (StoppedWorker{InstanceID: evidence.Initial.InstanceID, DeploymentID: evidence.Initial.DeploymentID}) {
		return receipt, errors.New("invalid hosted Task recovery evidence")
	}
	if evidence.Result != (SmokeResult{TaskID: evidence.TaskID, Status: "completed", Text: smokeReportResult}) ||
		evidence.Observed != (ObservedResult{TaskID: evidence.TaskID, Namespace: scope.Namespace, Attempt: 2, TaskRows: 1, CompletedResultRows: 1}) {
		return receipt, errors.New("invalid hosted Task completion evidence")
	}
	if !evidence.CleanupConfirmed || cleanup != (CleanupObservation{}) {
		return receipt, errors.New("hosted Task cleanup remains incomplete")
	}
	receipt = RecoveryReceipt{
		Version: 1, ArchiveSHA256: archives, RunID: runID, TaskID: evidence.TaskID,
		Initial: receiptClaim(evidence.Initial), StoppedInstanceID: evidence.Stopped.InstanceID, Recovered: receiptClaim(evidence.Recovered),
		Result: ReceiptResult(evidence.Result), Observer: ReceiptObservation{Namespace: evidence.Observed.Namespace, TaskID: evidence.Observed.TaskID, Attempt: evidence.Observed.Attempt, TaskRows: evidence.Observed.TaskRows, CompletedResultRows: evidence.Observed.CompletedResultRows}, Cleanup: cleanup,
	}
	return receipt, nil
}

func receiptClaim(c WorkerClaim) ReceiptClaim {
	return ReceiptClaim{TaskID: c.TaskID, Status: c.Status, Attempt: c.Attempt, DeploymentID: c.DeploymentID, InstanceID: c.InstanceID}
}
