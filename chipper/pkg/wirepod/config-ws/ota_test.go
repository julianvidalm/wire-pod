package webserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// otaDir points the OTA directory at a temp dir and fails the test on any outbound request.
func otaDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("V3CTOR_DATA", dir)
	defaultTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = defaultTransport })
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL)
		return nil, nil
	})
	ota := filepath.Join(dir, "robot", "ota")
	os.MkdirAll(ota, 0o755)
	return ota
}

func getOTA(path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	apiHandler(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestOTAIsServedFromTheLocalDirectory(t *testing.T) {
	dir := otaDir(t)
	os.WriteFile(filepath.Join(dir, "wireos-3.0.2.ota"), []byte("firmware bytes"), 0o644)

	rec := getOTA("/api/get_ota/wireos-3.0.2.ota")

	if rec.Code != http.StatusOK || rec.Body.String() != "firmware bytes" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body)
	}
}

func TestMissingOTASaysWhereToPutIt(t *testing.T) {
	dir := otaDir(t)

	rec := getOTA("/api/get_ota/vicos-2.0.1.6076ep.ota")

	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "vicos-2.0.1.6076ep.ota is not in "+dir) {
		t.Fatalf("status %d body %q", rec.Code, rec.Body)
	}
}

func TestOTANameCannotLeaveTheDirectory(t *testing.T) {
	dir := otaDir(t)
	os.WriteFile(filepath.Join(filepath.Dir(dir), "secret"), []byte("secret"), 0o644)

	for _, path := range []string{"/api/get_ota/../secret", "/api/get_ota/..%2Fsecret", "/api/get_ota/"} {
		rec := getOTA(path)
		if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "secret\n") || rec.Body.String() == "secret" {
			t.Fatalf("%s: status %d body %q", path, rec.Code, rec.Body)
		}
	}
}
