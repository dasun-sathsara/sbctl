package platform

import "testing"

func TestRuntimePaths(t *testing.T) {
	darwin := Darwin()
	if darwin.ProfilesDir != "/usr/local/etc/sing-box/profiles" {
		t.Fatalf("darwin profiles dir = %s", darwin.ProfilesDir)
	}
	linux := Linux()
	if linux.ProfilesDir != "/etc/sing-box/profiles" {
		t.Fatalf("linux profiles dir = %s", linux.ProfilesDir)
	}
	windows := Windows()
	if windows.ActiveNamePath == "" {
		t.Fatal("windows active name path is empty")
	}
}
