package webserver

// Endpoints for the Bluetooth setup page in webroot/setup/ (Digital Dream Labs' vector-web-setup, run locally).
// They replace what that page used to get from its Express server and from the accounts cloud.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kercre123/wire-pod/chipper/pkg/vars"
)

// setupSessionToken is the fixed token the page hands the robot (doAnkiAuth); the robot then authenticates against
// this server, which accepts it. The BLE setup in setup/ble.go uses the same one.
const setupSessionToken = "2vMhFgktH3Jrbemm2WHkfGN"

// setupLANAddress is the address robots reach this server at. OTA URLs use it, not localhost: the robot downloads
// the file itself over Wi-Fi.
var setupLANAddress = func() string { return vars.GetOutboundIP().String() }

func handleSetup(w http.ResponseWriter, name string) {
	switch name {
	case "sessions":
		writeSetupJSON(w, map[string]any{"session": map[string]string{
			"session_token": setupSessionToken,
			"user_id":       "wire-pod",
			"scope":         "user",
		}})
	case "otas":
		writeSetupJSON(w, struct {
			BaseURL string   `json:"base_url"`
			Files   []string `json:"files"`
		}{"http://" + setupLANAddress() + ":" + vars.WebPort + "/api/get_ota/", localOTAs()})
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// localOTAs lists the .ota files in <data dir>/robot/ota (the ones /api/get_ota/ serves), sorted; none when the
// directory is missing.
func localOTAs() []string {
	files := []string{}
	entries, _ := os.ReadDir(filepath.Join(vars.DataDir(), "robot", "ota"))
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".ota") && !strings.HasPrefix(e.Name(), ".") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files
}

func writeSetupJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
