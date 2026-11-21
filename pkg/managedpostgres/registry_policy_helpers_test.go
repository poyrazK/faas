package managedpostgres

// mutatePolicy edits the registry's effective policy in a test, the way a
// live policy would replace it: a copy is changed and stored whole.
func (r *Registry) mutatePolicy(change func(*effectivePolicy)) {
	if r.policy == nil {
		r.policy = &livePolicyState{}
	}
	next := *r.effective()
	change(&next)
	r.policy.current.Store(&next)
}

// registryWithUsage is a registry that serves only a usage policy.
func registryWithUsage(policy UsagePolicy) *Registry {
	r := &Registry{}
	r.mutatePolicy(func(e *effectivePolicy) { e.usage = policy })
	return r
}
