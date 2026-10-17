package webserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kercre123/wire-pod/chipper/pkg/vars"
)

// setupLAN fixes the LAN address and port the setup endpoints report, so the tests do not depend on this machine's network.
func setupLAN(t *testing.T, ip, port string) {
	t.Helper()
	oldAddress, oldPort := setupLANAddress, vars.WebPort
	t.Cleanup(func() { setupLANAddress, vars.WebPort = oldAddress, oldPort })
	setupLANAddress = func() string { return ip }
	vars.WebPort = port
}

func decodeJSON(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
}

func TestSetupSessionIsTheLocalStubToken(t *testing.T) {
	otaDir(t) // fails on any outbound request

	rec := getOTA("/api/setup/sessions")

	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var got struct {
		Session struct {
			SessionToken string `json:"session_token"`
			UserID       string `json:"user_id"`
			Scope        string `json:"scope"`
		} `json:"session"`
	}
	decodeJSON(t, rec.Body.Bytes(), &got)
	if got.Session.SessionToken != "2vMhFgktH3Jrbemm2WHkfGN" || got.Session.UserID == "" || got.Session.Scope != "user" {
		t.Fatalf("session %+v", got.Session)
	}
}

func TestSetupOTAsListsTheLocalFirmwareWithTheLANAddress(t *testing.T) {
	dir := otaDir(t)
	setupLAN(t, "192.168.68.20", "8080")
	for _, name := range []string{"wireos-3.0.2.ota", "vicos-2.0.1.6076ep.ota", "notes.txt", ".hidden.ota"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	os.Mkdir(filepath.Join(dir, "folder.ota"), 0o755)

	rec := getOTA("/api/setup/otas")

	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var got struct {
		BaseURL string   `json:"base_url"`
		Files   []string `json:"files"`
	}
	decodeJSON(t, rec.Body.Bytes(), &got)
	if got.BaseURL != "http://192.168.68.20:8080/api/get_ota/" {
		t.Fatalf("base_url %q", got.BaseURL)
	}
	if len(got.Files) != 2 || got.Files[0] != "vicos-2.0.1.6076ep.ota" || got.Files[1] != "wireos-3.0.2.ota" {
		t.Fatalf("files %q", got.Files)
	}
}

func TestSetupOTAsIsEmptyWhenTheDirectoryIsMissing(t *testing.T) {
	dir := otaDir(t)
	os.RemoveAll(dir)
	setupLAN(t, "10.0.0.5", "9090")

	rec := getOTA("/api/setup/otas")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	// An empty list, not null: the page iterates it.
	var raw map[string]json.RawMessage
	decodeJSON(t, rec.Body.Bytes(), &raw)
	if string(raw["files"]) != "[]" || string(raw["base_url"]) != `"http://10.0.0.5:9090/api/get_ota/"` {
		t.Fatalf("body %s", rec.Body)
	}
}

func TestUnknownSetupEndpointIsNotFound(t *testing.T) {
	otaDir(t)

	if rec := getOTA("/api/setup/nothing"); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}
