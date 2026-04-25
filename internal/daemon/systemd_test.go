package daemon

import "testing"

func TestSystemdMissingOrInactiveMapping(t *testing.T) {
	cases := []string{
		"Unit sing-box.service could not be found.",
		"inactive",
		"service not loaded",
	}
	for _, tc := range cases {
		if !isSystemdMissingOrInactive(tc) {
			t.Fatalf("expected stopped mapping for %q", tc)
		}
	}
}
