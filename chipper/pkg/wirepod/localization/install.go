package localization

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/julianvidalm/wire-pod/chipper/pkg/logger"
	"github.com/julianvidalm/wire-pod/chipper/pkg/vars"
)

// ModelSourceDir holds the Vosk model zips (vosk-model-small-*.zip) that can be installed, and optionally a
// SHA256SUMS file ("<sha256>  <file>" lines) that installs are checked against. Models are never downloaded:
// a language whose zip is missing fails with a message that says where to put it. VOSK_MODEL_DIR overrides it.
var ModelSourceDir = modelSourceDir()

func modelSourceDir() string {
	if dir := os.Getenv("VOSK_MODEL_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(vars.DataDir(), "vosk", "models")
}

// DownloadStatus is the install progress the web UI polls: "not downloading", progress text, "success" or
// "error: ...". (The name predates local installs; the UI contract is unchanged.)
var DownloadStatus string = "not downloading"

var modelFiles = map[string]string{
	"en-US": "vosk-model-small-en-us-0.15.zip",
	"it-IT": "vosk-model-small-it-0.22.zip",
	"es-ES": "vosk-model-small-es-0.42.zip",
	"fr-FR": "vosk-model-small-fr-0.22.zip",
	"de-DE": "vosk-model-small-de-0.15.zip",
	"pt-BR": "vosk-model-small-pt-0.3.zip",
	"pl-PL": "vosk-model-small-pl-0.22.zip",
	"zh-CN": "vosk-model-small-cn-0.22.zip",
	"tr-TR": "vosk-model-small-tr-0.3.zip",
	"ru-RU": "vosk-model-small-ru-0.22.zip",
	"nt-NL": "vosk-model-small-nl-0.22.zip",
	"uk-UA": "vosk-model-small-uk-v3-small.zip",
	"vi-VN": "vosk-model-small-vn-0.4.zip",
	"ko-KR": "vosk-model-small-ko-0.22.zip",
}

// InstallVoskModel installs the model for language from ModelSourceDir into vars.VoskModelPath/<language>/model,
// makes it the active language and reloads the recognizer. Progress and the result go to DownloadStatus.
func InstallVoskModel(language string) {
	file, ok := modelFiles[language]
	if !ok {
		fail("no Vosk model for language " + language)
		return
	}
	src := filepath.Join(ModelSourceDir, file)
	if _, err := os.Stat(src); err != nil {
		fail(fmt.Sprintf("%s is not in %s; copy it there and try again", file, ModelSourceDir))
		return
	}
	DownloadStatus = "Checking " + file
	if err := checkSum(src); err != nil {
		fail(err.Error())
		return
	}

	DownloadStatus = "Unpacking model..."
	dest := filepath.Join(vars.VoskModelPath, language)
	staging := dest + ".partial"
	os.RemoveAll(staging)
	if err := unzip(src, staging); err != nil {
		os.RemoveAll(staging)
		fail(err.Error())
		return
	}
	// The zip holds one directory named like the file; it becomes <language>/model. Check that before touching
	// an installed model, and swap the old one out only once the new one is in place.
	unpacked := filepath.Join(staging, strings.TrimSuffix(file, ".zip"))
	if info, err := os.Stat(unpacked); err != nil || !info.IsDir() {
		os.RemoveAll(staging)
		fail("unexpected layout in " + file + ": no " + filepath.Base(unpacked) + " directory")
		return
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		os.RemoveAll(staging)
		fail(err.Error())
		return
	}
	model, old := filepath.Join(dest, "model"), filepath.Join(dest, "model.old")
	os.RemoveAll(old)
	if _, err := os.Stat(model); err == nil {
		if err := os.Rename(model, old); err != nil {
			os.RemoveAll(staging)
			fail(err.Error())
			return
		}
	}
	if err := os.Rename(unpacked, model); err != nil {
		os.Rename(old, model) // put the previous model back
		os.RemoveAll(staging)
		fail(err.Error())
		return
	}
	os.RemoveAll(old)
	os.RemoveAll(staging)

	vars.DownloadedVoskModels = append(vars.DownloadedVoskModels, language)
	DownloadStatus = "Reloading voice processor"
	vars.APIConfig.STT.Language = language
	vars.APIConfig.PastInitialSetup = true
	vars.WriteConfigToDisk()
	ReloadVosk()
	logger.Println("Installed Vosk model " + file + " for " + language)
	DownloadStatus = "success"
}

func fail(msg string) {
	logger.Println("Vosk model install: " + msg)
	DownloadStatus = "error: " + msg
}

// checkSum verifies file against SHA256SUMS next to it, when that file lists it.
func checkSum(file string) error {
	sums, err := os.Open(filepath.Join(filepath.Dir(file), "SHA256SUMS"))
	if err != nil {
		return nil // no manifest: nothing to check against
	}
	defer sums.Close()
	name := filepath.Base(file)
	var want string
	scanner := bufio.NewScanner(sums)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			want = strings.ToLower(fields[0])
		}
	}
	if want == "" {
		return nil
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s does not match SHA256SUMS (got %s)", name, got)
	}
	return nil
}

// unzip extracts file into dest, refusing entries that would land outside dest.
func unzip(file, dest string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer zr.Close()
	root := filepath.Clean(dest) + string(os.PathSeparator)
	for _, f := range zr.File {
		path := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(path, root) {
			return fmt.Errorf("%s: entry %q points outside the model directory", filepath.Base(file), f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := extract(f, path); err != nil {
			return err
		}
	}
	return nil
}

func extract(f *zip.File, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
