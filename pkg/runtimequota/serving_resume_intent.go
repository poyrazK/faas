package runtimequota

// adr: 626
import "github.com/google/uuid"

// ServingResumeIntent identifies one immutable serving authorization on an
// existing physical attempt. It is bookkeeping, not native pause/stop proof.
type ServingResumeIntent struct {
	ID   string
	Seat ServingSeat
}

func NewServingResumeIntent(p CapacityPermit, id string, rollout bool) (ServingResumeIntent, error) {
	i := ServingResumeIntent{ID: id, Seat: ServingSeat{Permit: p, ID: resumeSeatID(p.ID, id), Rollout: rollout}}
	if i.Validate() != nil {
		return ServingResumeIntent{}, ErrInvalid
	}
	return i, nil
}

func (i ServingResumeIntent) Validate() error {
	if !validID(i.ID) || i.Seat.Validate() != nil || i.Seat.Permit.Grant.Validate() != nil || i.Seat.ID != resumeSeatID(i.Seat.Permit.ID, i.ID) {
		return ErrInvalid
	}
	return nil
}

func (i ServingResumeIntent) Placement() Placement {
	s := i.Seat
	return Placement{Permit: s.Permit, Seat: &s}
}

func resumeSeatID(permit, intent string) string {
	return uuid.NewSHA1(uuid.Nil, []byte("gregale/serving-resume/v1/"+permit+"/"+intent)).String()
}
