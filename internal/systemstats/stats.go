package systemstats

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Stats struct {
	CollectedAt time.Time      `json:"collected_at"`
	CPUCount    int            `json:"cpu_count"`
	LoadAvg     string         `json:"load_avg,omitempty"`
	MemoryKB    map[string]int `json:"memory_kb"`
}

func Collect() (Stats, error) {
	mem, err := readMeminfo()
	if err != nil {
		return Stats{}, err
	}
	return Stats{
		CollectedAt: time.Now().UTC(),
		CPUCount:    runtime.NumCPU(),
		LoadAvg:     readLoadAvg(),
		MemoryKB:    mem,
	}, nil
}

func readMeminfo() (map[string]int, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		// macOS/dev fallback. Real installer target is Ubuntu 24.04.
		return map[string]int{}, nil
	}
	defer f.Close()

	out := map[string]int{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		parts := strings.Fields(strings.TrimSuffix(s.Text(), ":"))
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		value, _ := strconv.Atoi(parts[1])
		out[key] = value
	}
	return out, s.Err()
}

func readLoadAvg() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
