package profilecapture

import "github.com/onebox-faas/faas/pkg/api"

// CreateProblem exposes createProblem to the external profilecapture_test
// package, so the store rejections that cannot be reached cheaply through a
// plan's real limits still have their problem shape pinned.
var CreateProblem = createProblem

// HoldMergeSlots takes every merge slot until the returned func releases them.
func (s *Service) HoldMergeSlots() (release func()) {
	s.mergeOnce.Do(func() { s.mergeSlots = make(chan struct{}, api.ProfileCaptureMaxConcurrentMerges) })
	for range api.ProfileCaptureMaxConcurrentMerges {
		s.mergeSlots <- struct{}{}
	}
	return func() {
		for range api.ProfileCaptureMaxConcurrentMerges {
			<-s.mergeSlots
		}
	}
}
