package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
)

func TestVersionInfoReportsTheRunningBuildWithoutAskingGitHub(t *testing.T) {
	vars.CommitSHA = "abc1234"
	vars.VersionFile = t.TempDir() + "/version" // no packaged version file: a source build
	// Any outbound request fails the test: the version comes from the build, not from the network.
	defaultTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = defaultTransport })
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL)
		return nil, nil
	})

	rec := httptest.NewRecorder()
	apiHandler(rec, httptest.NewRequest(http.MethodGet, "/api/get_version_info", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	want := map[string]any{"commit": "abc1234", "version": ""}
	if len(got) != len(want) || got["commit"] != want["commit"] || got["version"] != want["version"] {
		t.Fatalf("version info %v, want %v", got, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
