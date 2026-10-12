package state

// adr: 801
import (
	"context"
	"encoding/json"
	"time"
)

const (
	RegionalFloorApp        = "app"
	RegionalFloorDeployment = "deployment"
)

// Customer intent only; neither a capacity entitlement nor native residency.
// Keep the original policy, RAM and deployment ordering after collection.
type RegionalRuntimeFloorApp struct {
	RAMMB, MinInstances int
	Status              AppStatus
	ScalingPolicy       *ScalingPolicy
}

type RegionalRuntimeFloorDeployment struct {
	MinInstances int
	Status       DeploymentStatus
	Scope        string
	CreatedAt    time.Time
}

type RegionalRuntimeFloorInterval struct {
	ID                    int64
	AppID, ObjectID, Kind string
	StartedAt             time.Time
	EndedAt               *time.Time
	App                   *RegionalRuntimeFloorApp
	Deployment            *RegionalRuntimeFloorDeployment
}

type RegionalRuntimeFloorHistoryStore interface {
	RegionalRuntimeFloorHistoryStart(context.Context) (time.Time, error)
	// A page is retained intent, not complete minute coverage. Read under the
	// app quota journal lock when sealing and reconcile original allocations,
	// actual serving intervals and deployment/environment eligibility.
	ListRegionalRuntimeFloorIntervals(context.Context, string, time.Time, int64, int) ([]RegionalRuntimeFloorInterval, error)
}

func regionalFloorAppClaims(app App) []byte {
	raw, _ := json.Marshal(RegionalRuntimeFloorApp{RAMMB: app.RAMMB, MinInstances: app.MinInstances, Status: app.Status, ScalingPolicy: regionalFloorPolicy(app.ScalingPolicy)})
	return raw
}

// Other scaling knobs do not change the minimum-instance billing promise.
func regionalFloorPolicy(p *ScalingPolicy) *ScalingPolicy {
	if p == nil {
		return &ScalingPolicy{}
	}
	return &ScalingPolicy{MinInstances: p.MinInstances, Timezone: p.Timezone, Schedules: append([]ScalingSchedule(nil), p.Schedules...)}
}

func regionalFloorDeploymentClaims(dep Deployment) []byte {
	raw, _ := json.Marshal(RegionalRuntimeFloorDeployment{MinInstances: dep.MinInstances, Status: dep.Status, Scope: dep.Scope, CreatedAt: dep.CreatedAt})
	return raw
}

func regionalRuntimeFloorDecode(id int64, app, object, kind string, start time.Time, end *time.Time, claims []byte) (RegionalRuntimeFloorInterval, error) {
	r := RegionalRuntimeFloorInterval{ID: id, AppID: app, ObjectID: object, Kind: kind, StartedAt: start, EndedAt: cloneTimePtr(end)}
	switch kind {
	case RegionalFloorApp:
		r.App = &RegionalRuntimeFloorApp{}
		if json.Unmarshal(claims, r.App) != nil {
			return RegionalRuntimeFloorInterval{}, ErrConflict
		}
	case RegionalFloorDeployment:
		r.Deployment = &RegionalRuntimeFloorDeployment{}
		if json.Unmarshal(claims, r.Deployment) != nil {
			return RegionalRuntimeFloorInterval{}, ErrConflict
		}
	default:
		return RegionalRuntimeFloorInterval{}, ErrConflict
	}
	return r, nil
}
