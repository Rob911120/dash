package dash

import (
	"bufio"
	"net"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// SystemState captures the machine's state at a point in time.
type SystemState struct {
	// OS & Kernel
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`

	// Uptime & Load
	UptimeSec int64     `json:"uptime_sec,omitempty"`
	LoadAvg   []float64 `json:"load_avg,omitempty"` // 1, 5, 15 min

	// Memory
	MemTotalMB int64 `json:"mem_total_mb,omitempty"`
	MemFreeMB  int64 `json:"mem_free_mb,omitempty"`
	MemUsedPct int   `json:"mem_used_pct,omitempty"`

	// Disk (cwd partition)
	DiskTotalGB int64 `json:"disk_total_gb,omitempty"`
	DiskFreeGB  int64 `json:"disk_free_gb,omitempty"`
	DiskUsedPct int   `json:"disk_used_pct,omitempty"`

	// Network
	Network *NetworkState `json:"network,omitempty"`
}

// NetworkState holds network-related state.
type NetworkState struct {
	Interfaces  []NetInterface `json:"interfaces,omitempty"`
	ListenPorts []ListenPort   `json:"listen_ports,omitempty"`
}

// NetInterface represents a network interface.
type NetInterface struct {
	Name string   `json:"name"`
	IPs  []string `json:"ips,omitempty"`
	Up   bool     `json:"up"`
}

// ListenPort represents a listening port.
type ListenPort struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"` // tcp, tcp6
}

// ProcessContext captures the current process context.
type ProcessContext struct {
	PID       int    `json:"pid"`
	PPID      int    `json:"ppid"`
	User      string `json:"user,omitempty"`
	CWD       string `json:"cwd"`
	NumGo     int    `json:"num_goroutines,omitempty"`
}

// CaptureSystemState captures the current system state.
func CaptureSystemState() *SystemState {
	state := &SystemState{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}

	// Hostname
	if hostname, err := os.Hostname(); err == nil {
		state.Hostname = hostname
	}

	// Linux-specific: read from /proc
	if runtime.GOOS == "linux" {
		state.UptimeSec = readUptime()
		state.LoadAvg = readLoadAvg()
		readMemInfo(state)
	}

	// Disk usage for cwd
	readDiskUsage(state)

	// Network interfaces and listening ports
	state.Network = captureNetworkState()

	return state
}

// CaptureProcessContext captures the current process context.
func CaptureProcessContext() *ProcessContext {
	ctx := &ProcessContext{
		PID:   os.Getpid(),
		PPID:  os.Getppid(),
		NumGo: runtime.NumGoroutine(),
	}

	if cwd, err := os.Getwd(); err == nil {
		ctx.CWD = cwd
	}

	if u, err := user.Current(); err == nil {
		ctx.User = u.Username
	}

	return ctx
}

// readUptime reads system uptime from /proc/uptime.
func readUptime() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}

	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0
	}

	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}

	return int64(uptime)
}

// readLoadAvg reads load averages from /proc/loadavg.
func readLoadAvg() []float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil
	}

	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return nil
	}

	loads := make([]float64, 0, 3)
	for i := 0; i < 3; i++ {
		load, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return nil
		}
		loads = append(loads, load)
	}

	return loads
}

// readMemInfo reads memory info from /proc/meminfo.
func readMemInfo(state *SystemState) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()

	var memTotal, memFree, memAvailable, buffers, cached int64

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		// Values in /proc/meminfo are in kB
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}

		switch fields[0] {
		case "MemTotal:":
			memTotal = value
		case "MemFree:":
			memFree = value
		case "MemAvailable:":
			memAvailable = value
		case "Buffers:":
			buffers = value
		case "Cached:":
			cached = value
		}
	}

	state.MemTotalMB = memTotal / 1024
	// Use MemAvailable if present (more accurate), otherwise estimate
	if memAvailable > 0 {
		state.MemFreeMB = memAvailable / 1024
	} else {
		state.MemFreeMB = (memFree + buffers + cached) / 1024
	}

	if state.MemTotalMB > 0 {
		used := state.MemTotalMB - state.MemFreeMB
		state.MemUsedPct = int(used * 100 / state.MemTotalMB)
	}
}

// readDiskUsage reads disk usage for the current working directory.
func readDiskUsage(state *SystemState) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(cwd, &stat); err != nil {
		return
	}

	// Calculate disk usage
	totalBytes := stat.Blocks * uint64(stat.Bsize)
	freeBytes := stat.Bavail * uint64(stat.Bsize) // Bavail = available to non-root

	state.DiskTotalGB = int64(totalBytes / (1024 * 1024 * 1024))
	state.DiskFreeGB = int64(freeBytes / (1024 * 1024 * 1024))

	if state.DiskTotalGB > 0 {
		used := state.DiskTotalGB - state.DiskFreeGB
		state.DiskUsedPct = int(used * 100 / state.DiskTotalGB)
	}
}

// captureNetworkState captures network interfaces and listening ports.
func captureNetworkState() *NetworkState {
	netState := &NetworkState{}

	// Network interfaces
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			// Skip loopback and down interfaces for brevity
			if iface.Flags&net.FlagLoopback != 0 {
				continue
			}

			ni := NetInterface{
				Name: iface.Name,
				Up:   iface.Flags&net.FlagUp != 0,
			}

			addrs, err := iface.Addrs()
			if err == nil {
				for _, addr := range addrs {
					// Get IP address without the network mask
					if ipnet, ok := addr.(*net.IPNet); ok {
						if ip4 := ipnet.IP.To4(); ip4 != nil {
							ni.IPs = append(ni.IPs, ip4.String())
						} else if ip6 := ipnet.IP.To16(); ip6 != nil && !ipnet.IP.IsLinkLocalUnicast() {
							ni.IPs = append(ni.IPs, ip6.String())
						}
					}
				}
			}

			if len(ni.IPs) > 0 || ni.Up {
				netState.Interfaces = append(netState.Interfaces, ni)
			}
		}
	}

	// Listening ports (Linux specific)
	if runtime.GOOS == "linux" {
		netState.ListenPorts = readListeningPorts()
	}

	return netState
}

// readListeningPorts reads listening TCP ports from /proc/net/tcp and /proc/net/tcp6.
func readListeningPorts() []ListenPort {
	var ports []ListenPort

	// Read IPv4 TCP
	tcp4 := parseNetTCP("/proc/net/tcp", "tcp")
	ports = append(ports, tcp4...)

	// Read IPv6 TCP
	tcp6 := parseNetTCP("/proc/net/tcp6", "tcp6")
	ports = append(ports, tcp6...)

	return ports
}

// parseNetTCP parses /proc/net/tcp or /proc/net/tcp6 for listening ports.
func parseNetTCP(path, proto string) []ListenPort {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var ports []ListenPort
	seen := make(map[int]bool)

	scanner := bufio.NewScanner(f)
	scanner.Scan() // Skip header line

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}

		// State is field 3 (0-indexed), 0A = LISTEN
		if fields[3] != "0A" {
			continue
		}

		// local_address is field 1, format: IP:PORT (hex)
		localAddr := fields[1]
		parts := strings.Split(localAddr, ":")
		if len(parts) != 2 {
			continue
		}

		portHex := parts[1]
		port64, err := strconv.ParseInt(portHex, 16, 32)
		if err != nil {
			continue
		}
		port := int(port64)

		// Avoid duplicates
		if seen[port] {
			continue
		}
		seen[port] = true

		ports = append(ports, ListenPort{
			Port:  port,
			Proto: proto,
		})
	}

	return ports
}
