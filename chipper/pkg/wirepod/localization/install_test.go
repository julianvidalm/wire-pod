package localization

import (
	"archive/zip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kercre123/wire-pod/chipper/pkg/vars"
)

// setup points the model source, the installed models and the config file at temp dirs, and fails the test on
// any network request: models come from the local directory only.
func setup(t *testing.T) (source, models string) {
	t.Helper()
	source, models = t.TempDir(), t.TempDir()
	oldSource, oldModels, oldConfig := ModelSourceDir, vars.VoskModelPath, vars.ApiConfigPath
	oldDownloaded, oldLanguage, oldTransport := vars.DownloadedVoskModels, vars.APIConfig.STT.Language, http.DefaultTransport
	t.Cleanup(func() {
		ModelSourceDir, vars.VoskModelPath, vars.ApiConfigPath = oldSource, oldModels, oldConfig
		vars.DownloadedVoskModels, vars.APIConfig.STT.Language, http.DefaultTransport = oldDownloaded, oldLanguage, oldTransport
		DownloadStatus = "not downloading"
	})
	ModelSourceDir, vars.VoskModelPath = source, models
	vars.ApiConfigPath = filepath.Join(t.TempDir(), "apiConfig.json")
	vars.DownloadedVoskModels = nil
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected network request to %s", r.URL)
		return nil, nil
	})
	return source, models
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// writeZip creates a zip with the given entries (name -> content; a name ending in "/" is a directory).
func writeZip(t *testing.T, path string, entries map[string]string) []byte {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(name, "/") {
			w.Write([]byte(entries[name]))
		}
	}
	zw.Close()
	f.Close()
	b, _ := os.ReadFile(path)
	return b
}

func spanishModel(t *testing.T, source string) []byte {
	return writeZip(t, filepath.Join(source, "vosk-model-small-es-0.42.zip"), map[string]string{
		"vosk-model-small-es-0.42/":             "",
		"vosk-model-small-es-0.42/am/":          "",
		"vosk-model-small-es-0.42/am/final.mdl": "acoustic model",
	})
}

func TestInstallsAModelFromTheLocalDirectory(t *testing.T) {
	source, models := setup(t)
	zipBytes := spanishModel(t, source)
	sum := sha256.Sum256(zipBytes)
	os.WriteFile(filepath.Join(source, "SHA256SUMS"), []byte(fmt.Sprintf("%x  vosk-model-small-es-0.42.zip\n", sum)), 0o644)

	InstallVoskModel("es-ES")

	if DownloadStatus != "success" {
		t.Fatalf("status %q", DownloadStatus)
	}
	got, err := os.ReadFile(filepath.Join(models, "es-ES", "model", "am", "final.mdl"))
	if err != nil || string(got) != "acoustic model" {
		t.Fatalf("installed model file: %q, %v", got, err)
	}
	if vars.APIConfig.STT.Language != "es-ES" || !slices.Contains(vars.DownloadedVoskModels, "es-ES") {
		t.Fatalf("language %q, installed %v", vars.APIConfig.STT.Language, vars.DownloadedVoskModels)
	}
	if config, err := os.ReadFile(vars.ApiConfigPath); err != nil || !strings.Contains(string(config), `"es-ES"`) {
		t.Fatalf("config not saved with the language: %v", err)
	}
}

func TestMissingModelZipSaysWhereToPutIt(t *testing.T) {
	source, models := setup(t)

	InstallVoskModel("es-ES")

	want := "error: vosk-model-small-es-0.42.zip is not in " + source
	if !strings.HasPrefix(DownloadStatus, want) {
		t.Fatalf("status %q, want prefix %q", DownloadStatus, want)
	}
	if entries, _ := os.ReadDir(models); len(entries) != 0 {
		t.Fatalf("something was installed: %v", entries)
	}
}

func TestChecksumMismatchIsRejected(t *testing.T) {
	source, models := setup(t)
	spanishModel(t, source)
	os.WriteFile(filepath.Join(source, "SHA256SUMS"), []byte(strings.Repeat("0", 64)+"  vosk-model-small-es-0.42.zip\n"), 0o644)

	InstallVoskModel("es-ES")

	if !strings.HasPrefix(DownloadStatus, "error: vosk-model-small-es-0.42.zip does not match SHA256SUMS") {
		t.Fatalf("status %q", DownloadStatus)
	}
	if _, err := os.Stat(filepath.Join(models, "es-ES")); !os.IsNotExist(err) {
		t.Fatalf("model installed despite the checksum: %v", err)
	}
}

func TestZipEntriesCannotEscapeTheModelDirectory(t *testing.T) {
	source, models := setup(t)
	writeZip(t, filepath.Join(source, "vosk-model-small-es-0.42.zip"), map[string]string{
		"vosk-model-small-es-0.42/../../evil.txt": "pwned",
	})

	InstallVoskModel("es-ES")

	if !strings.HasPrefix(DownloadStatus, "error:") {
		t.Fatalf("status %q", DownloadStatus)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(models), "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("a zip entry was written outside the models directory")
	}
}

func TestUnknownLanguageEndsWithAnError(t *testing.T) {
	setup(t)

	InstallVoskModel("xx-XX")

	if DownloadStatus != "error: no Vosk model for language xx-XX" {
		t.Fatalf("status %q", DownloadStatus)
	}
}

func TestAZipWithTheWrongLayoutKeepsTheInstalledModel(t *testing.T) {
	source, models := setup(t)
	installed := filepath.Join(models, "es-ES", "model", "am", "final.mdl")
	os.MkdirAll(filepath.Dir(installed), 0o755)
	os.WriteFile(installed, []byte("working model"), 0o644)
	writeZip(t, filepath.Join(source, "vosk-model-small-es-0.42.zip"), map[string]string{
		"some-other-name/am/final.mdl": "wrong layout",
	})

	InstallVoskModel("es-ES")

	if !strings.HasPrefix(DownloadStatus, "error: unexpected layout in vosk-model-small-es-0.42.zip") {
		t.Fatalf("status %q", DownloadStatus)
	}
	if got, err := os.ReadFile(installed); err != nil || string(got) != "working model" {
		t.Fatalf("installed model changed: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(models, "es-ES.partial")); !os.IsNotExist(err) {
		t.Fatalf("staging directory left behind: %v", err)
	}
}
