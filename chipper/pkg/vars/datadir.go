package vars

import (
	"os"
	"path/filepath"
)

// DataDir is the one local directory for the files wire-pod needs but does not build: Vosk models, libvosk,
// robot recovery files. Nothing is downloaded from other people's servers; whatever is missing is copied here.
// V3CTOR_DATA overrides it (a Docker volume, for example); otherwise $XDG_DATA_HOME/v3ctor or
// ~/.local/share/v3ctor.
func DataDir() string {
	if dir := os.Getenv("V3CTOR_DATA"); dir != "" {
		return dir
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "v3ctor")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "v3ctor-data")
	}
	return filepath.Join(home, ".local", "share", "v3ctor")
}
