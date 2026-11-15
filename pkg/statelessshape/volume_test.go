package statelessshape

import (
	"reflect"
	"testing"
)

func TestStatefulVolume(t *testing.T) {
	cases := []struct {
		instruction string
		want        []string
	}{
		{"VOLUME /tmp", nil},
		{`VOLUME ["/tmp"]`, nil},
		{"volume /var/tmp/cache /run/app /dev/shm", nil},
		{"VOLUME /tmp/../data", []string{"/tmp/../data"}},
		{"VOLUME /data", []string{"/data"}},
		{`VOLUME ["/tmp", "/var/lib/postgresql/data"]`, []string{"/var/lib/postgresql/data"}},
		{"VOLUME /tmpfiles", []string{"/tmpfiles"}},
		{"VOLUME $DATA_DIR", []string{"$DATA_DIR"}},
		{"VOLUME", []string{"(no path)"}},
		{`VOLUME ["/data"`, []string{`["/data"`}},
		{"RUN mkdir /data", nil},
	}
	for _, tc := range cases {
		if got := StatefulVolume(tc.instruction); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("StatefulVolume(%q) = %q, want %q", tc.instruction, got, tc.want)
		}
	}
}
