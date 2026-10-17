package vars

import (
	"path/filepath"
	"testing"
)

func TestDataDirPrefersV3ctorDataThenXDGThenHome(t *testing.T) {
	t.Setenv("HOME", "/home/robot")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("V3CTOR_DATA", "")
	if got := DataDir(); got != filepath.Join("/home/robot", ".local", "share", "v3ctor") {
		t.Fatalf("home default: %s", got)
	}
	t.Setenv("XDG_DATA_HOME", "/xdg")
	if got := DataDir(); got != "/xdg/v3ctor" {
		t.Fatalf("XDG: %s", got)
	}
	t.Setenv("V3CTOR_DATA", "/volume")
	if got := DataDir(); got != "/volume" {
		t.Fatalf("V3CTOR_DATA: %s", got)
	}
}
