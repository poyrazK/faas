package state

// manualTrafficExplicit is a deployment's traffic_percent_explicit after a
// manual traffic change (gregale traffic set/promote) moves it from prior to
// next percent. deployments_app_scope_live_uniq admits one stable serving row
// per scope and leaves explicitly weighted rows out, as 20260910215409952
// intends for manual splits; the traffic path never marked them, so on
// production-us rc.294 splitting or promoting between two completed releases
// answered 500 (Hunt 14 H14-71). A split member is explicit; a row that ends
// at 100 is the stable row again; one lowered to 0 is a retained stable
// revision, not a manual dark deployment (explicit at 0 means that to
// approvals, crons and service overrides). An unchanged row keeps its flag.
func manualTrafficExplicit(prior, next int, current bool) bool {
	if manualTrafficKeepsExplicit(prior, next) {
		return current
	}
	return next > 0 && next < 100
}

// manualTrafficKeepsExplicit reports a traffic move that leaves a row's flag
// as it was: an unchanged weight, or a whole move between 0 and 100. A
// runtime upgrade's cutover leaves its candidate explicit at 100 (its dark
// candidate is explicit at 0); rolling the upgrade back and reactivating it
// with traffic promote must keep that marker, or the upgrade can no longer
// certify its gateway drain. A whole move never creates a split, so it never
// needs the flag deployments_app_scope_live_uniq relies on.
func manualTrafficKeepsExplicit(prior, next int) bool {
	whole := func(percent int) bool { return percent == 0 || percent == 100 }
	return prior == next || whole(prior) && whole(next)
}
