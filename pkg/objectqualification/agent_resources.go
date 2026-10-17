package objectqualification

type agentSample struct {
	PrimaryRSS  int64 `json:"primary_rss"`
	FollowerRSS int64 `json:"follower_rss"`
	DiskBytes   int64 `json:"disk_bytes"`
	Errors      int64 `json:"errors"`
}

func (s *agentState) sample() (agentResponse, error) {
	result := &agentSample{}
	for _, target := range []struct {
		child *child
		rss   *int64
	}{{s.lab.primary, &result.PrimaryRSS}, {s.lab.follower, &result.FollowerRSS}} {
		if childHealthy(target.child) != nil {
			continue
		}
		rss, err := residentBytes(target.child.command.Process.Pid)
		if err != nil {
			result.Errors++
		} else {
			*target.rss = rss
		}
	}
	var err error
	result.DiskBytes, err = physicalFiles(s.lab.root)
	if err != nil {
		return agentResponse{}, err
	}
	return agentResponse{Sample: result}, nil
}
