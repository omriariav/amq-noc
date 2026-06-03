package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// syscallSignalZero is the signal-0 used for a liveness probe. Aliased so
// state.go can stay free of a direct syscall import in its probe definition.
const syscallSignalZero = syscall.Signal(0)

func itoa(n int) string { return strconv.Itoa(n) }

// presenceFile mirrors AMQ's presence.json shape.
type presenceFile struct {
	Schema   int       `json:"schema"`
	Handle   string    `json:"handle"`
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen"`
}

// wakeLockFile mirrors AMQ's .wake.lock JSON shape.
type wakeLockFile struct {
	PID     int       `json:"pid"`
	TTY     string    `json:"tty,omitempty"`
	Root    string    `json:"root,omitempty"`
	Started time.Time `json:"started"`
}

func presencePath(agentDir string) string {
	return filepath.Join(agentDir, "presence.json")
}

func wakeLockPath(agentDir string) string {
	return filepath.Join(agentDir, ".wake.lock")
}

func readPresence(agentDir string) (presenceFile, error) {
	data, err := os.ReadFile(presencePath(agentDir))
	if err != nil {
		return presenceFile{}, err
	}
	var pres presenceFile
	if err := json.Unmarshal(data, &pres); err != nil {
		return presenceFile{}, err
	}
	return pres, nil
}

func readWakeLock(agentDir string) (wakeLockFile, error) {
	data, err := os.ReadFile(wakeLockPath(agentDir))
	if err != nil {
		return wakeLockFile{}, err
	}
	var lock wakeLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return wakeLockFile{}, err
	}
	return lock, nil
}
