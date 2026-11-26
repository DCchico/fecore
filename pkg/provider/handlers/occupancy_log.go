package handlers

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// LogOccupancy records one provider claim or release. Times are this host's
// clocks. A release that cannot find the container uses outcome=ambiguous so
// the exporter can flag incomplete ownership instead of calling the container idle.
func LogOccupancy(kind, requestID, functionName, vdag, seq, containerID, ip, outcome string) {
	var mono, wall unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &mono); err != nil {
		log.Printf("OCCUPANCY kind=%s outcome=clock_error request_id=%s err=%v", kind, requestID, err)
		return
	}
	_ = unix.ClockGettime(unix.CLOCK_REALTIME, &wall)
	log.Printf("OCCUPANCY kind=%s mono_ns=%d realtime_ns=%d request_id=%s function=%s vdag=%s seq=%s container=%s ip=%s outcome=%s memory_current=%s",
		kind, mono.Nano(), wall.Nano(), requestID, functionName, vdag, seq, containerID, ip, outcome, readMemoryCurrent(containerID))
}

// readMemoryCurrent reads the leaf cgroup usage. A missing file stays missing
// and is never replaced with memory.max.
func readMemoryCurrent(containerID string) string {
	if containerID == "" || strings.Contains(containerID, "/") {
		return "missing"
	}
	path := filepath.Join("/sys/fs/cgroup/faasedge-fn", containerID, "memory.current")
	body, err := os.ReadFile(path)
	if err != nil {
		return "missing"
	}
	return strings.TrimSpace(string(body))
}
