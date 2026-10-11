package main

import (
	"flag"
	"fmt"
	"strconv"
	"time"
)

// secondsOrDuration is a --timeout value accepting bare seconds ("600", the
// historical form) or a Go duration ("10m", "90s") as rollback, realtime and
// platform-tenants already do. production-us hunt #4: `deployment wait
// --timeout 60s` failed with a raw flag parse error.
type secondsOrDuration int

func (v *secondsOrDuration) String() string {
	if v == nil {
		return "0"
	}
	return strconv.Itoa(int(*v))
}

func (v *secondsOrDuration) Set(raw string) error {
	if n, err := strconv.Atoi(raw); err == nil {
		*v = secondsOrDuration(n)
		return nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("want seconds (600) or a duration (10m, 90s)")
	}
	// Round a sub-second remainder up so "1500ms" never waits less than asked.
	*v = secondsOrDuration((d + time.Second - 1) / time.Second)
	return nil
}

// secondsOrDurationFlag defines a seconds-valued flag that also accepts a
// duration and returns the seconds, like fs.Int.
func secondsOrDurationFlag(fs *flag.FlagSet, name string, value int, usage string) *int {
	p := new(int)
	*p = value
	fs.Var((*secondsOrDuration)(p), name, usage)
	return p
}
