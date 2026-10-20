package initwirepod

import (
	"fmt"
	"math"
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
	halfDuplexDBEnv       = "CORTEX_HALF_DUPLEX_DB"       // speaker level (dBFS) that counts as talking, default -40
)

// Lowest and highest CORTEX_HALF_DUPLEX_DB accepted; outside it the default is used.
const (
	minHalfDuplexDB = -120
	maxHalfDuplexDB = 0
)

// halfDuplexConfig reads the half-duplex settings; it returns nil when half duplex is off, and describes them.
// A bad hangover or threshold does not turn the gate off: it is reported in warnings and its default is used.
// Only an unknown CORTEX_HALF_DUPLEX value is an error.
func halfDuplexConfig() (*cortex.HalfDuplexConfig, string, []string, error) {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv(halfDuplexEnv))); v {
	case "", "on":
	case "off":
		return nil, "off (" + halfDuplexEnv + "=off): the detector and the conversation hear the robot talk", nil, nil
	default:
		return nil, "", nil, fmt.Errorf("unknown %s %q (want on or off)", halfDuplexEnv, v)
	}
	cfg := cortex.DefaultHalfDuplexConfig()
	var warnings []string
	if v := os.Getenv(halfDuplexHangoverEnv); v != "" {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("%s %q is not a Go duration, using the default %v",
				halfDuplexHangoverEnv, v, cfg.Hangover))
		case d < 0:
			warnings = append(warnings, fmt.Sprintf("%s %v is negative, using the default %v",
				halfDuplexHangoverEnv, d, cfg.Hangover))
		default:
			cfg.Hangover = d
		}
	}
	if v := os.Getenv(halfDuplexDBEnv); v != "" {
		db, err := strconv.ParseFloat(v, 64)
		switch {
		case err != nil || math.IsNaN(db) || math.IsInf(db, 0):
			warnings = append(warnings, fmt.Sprintf("%s %q is not a number, using the default %g dBFS",
				halfDuplexDBEnv, v, cfg.Threshold))
		case db < minHalfDuplexDB || db > maxHalfDuplexDB:
			warnings = append(warnings, fmt.Sprintf("%s %v is outside %d..%d dBFS, using the default %g dBFS",
				halfDuplexDBEnv, db, minHalfDuplexDB, maxHalfDuplexDB, cfg.Threshold))
		default:
			cfg.Threshold = db
		}
	}
	desc := fmt.Sprintf("on: the detector and the conversation hear silence while the robot's speaker is above "+
		"%g dBFS and for %v after, the mic waits at most %v for the speaker audio", cfg.Threshold, cfg.Hangover,
		cfg.MaxDelay)
	return &cfg, desc, warnings, nil
}
