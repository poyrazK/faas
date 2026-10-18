package guest

import core "github.com/onebox-faas/faas/pkg/datarepair"

type Result struct {
	Evidence    *core.Evidence `json:"evidence,omitempty"`
	FailureCode string         `json:"failure_code,omitempty"`
}
