package initwirepod

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/julianvidalm/wire-pod/chipper/pkg/cortex"
)

// Half duplex: while the robot talks, the wake word detector and the conversation hear silence instead of the
// mic (see cortex.HalfDuplex). The robot is talking when its speaker frames are louder than the threshold.
const (
	halfDuplexEnv         = "CORTEX_HALF_DUPLEX"          // on (the default) or off
	halfDuplexHangoverEnv = "CORTEX_HALF_DUPLEX_HANGOVER" // Go duration the mic stays silenced after the sound, default 600ms
	halfDuplexDBEnv       = "CORTEX_HALF_DUPLEX_DB"       // speaker level (dBFS) that counts as talking, default -60
)

// halfDuplexConfig reads the half-duplex settings; it returns nil when half duplex is off, and describes them.
func halfDuplexConfig() (*cortex.HalfDuplexConfig, string, error) {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv(halfDuplexEnv))); v {
	case "", "on":
	case "off":
		return nil, "off (" + halfDuplexEnv + "=off): the detector and the conversation hear the robot talk", nil
	default:
		return nil, "", fmt.Errorf("unknown %s %q (want on or off)", halfDuplexEnv, v)
	}
	cfg := cortex.DefaultHalfDuplexConfig()
	if v := os.Getenv(halfDuplexHangoverEnv); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", halfDuplexHangoverEnv, err)
		}
		if d < 0 {
			return nil, "", fmt.Errorf("%s %v is negative", halfDuplexHangoverEnv, d)
		}
		cfg.Hangover = d
	}
	if v := os.Getenv(halfDuplexDBEnv); v != "" {
		db, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", halfDuplexDBEnv, err)
		}
		if db >= 0 {
			return nil, "", fmt.Errorf("%s %v is not below 0 dBFS", halfDuplexDBEnv, db)
		}
		cfg.Threshold = db
	}
	desc := fmt.Sprintf("on: the detector and the conversation hear silence while the robot's speaker is above "+
		"%g dBFS and for %v after, the mic waits at most %v for the speaker audio", cfg.Threshold, cfg.Hangover,
		cfg.MaxDelay)
	return &cfg, desc, nil
}
