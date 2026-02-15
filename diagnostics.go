package dash

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DiagnosticFunc is a function that returns diagnostic output.
type DiagnosticFunc func() (string, error)

// Diagnostics is a map of available diagnostic functions.
var Diagnostics = map[string]DiagnosticFunc{
	"uname":      diagUname,
	"os-release": diagOSRelease,
	"uptime":     diagUptime,
	"loadavg":    diagLoadAvg,
	"meminfo":    diagMemInfo,
	"df":         diagDiskFree,
	"ip-addr":    diagIPAddr,
	"hostname":   diagHostname,
	"kernel":     diagKernel,
}

// RunDiagnostic runs a named diagnostic and returns its output.
func RunDiagnostic(name string) (string, error) {
	fn, ok := Diagnostics[name]
	if !ok {
		return "", fmt.Errorf("unknown diagnostic: %s", name)
	}
	return fn()
}

// RunAllDiagnostics runs all diagnostics and returns a map of results.
func RunAllDiagnostics() map[string]string {
	results := make(map[string]string)
	for name, fn := range Diagnostics {
		output, err := fn()
		if err != nil {
			results[name] = fmt.Sprintf("error: %v", err)
		} else {
			results[name] = output
		}
	}
	return results
}

// diagUname returns uname -a output.
func diagUname() (string, error) {
	out, err := exec.Command("uname", "-a").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// diagOSRelease returns /etc/os-release content.
func diagOSRelease() (string, error) {
	// Try os-release first, fall back to lsb-release
	paths := []string{"/etc/os-release", "/etc/lsb-release"}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err == nil {
			return parseOSRelease(string(data)), nil
		}
	}
	return "", fmt.Errorf("no os-release found")
}

// parseOSRelease extracts key info from os-release format.
func parseOSRelease(content string) string {
	var name, version, id string
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			name = strings.Trim(line[12:], "\"")
		} else if strings.HasPrefix(line, "VERSION_ID=") {
			version = strings.Trim(line[11:], "\"")
		} else if strings.HasPrefix(line, "ID=") {
			id = strings.Trim(line[3:], "\"")
		}
	}

	if name != "" {
		return name
	}
	if id != "" && version != "" {
		return fmt.Sprintf("%s %s", id, version)
	}
	return content
}

// diagUptime returns uptime from /proc/uptime.
func diagUptime() (string, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		// Try uptime command as fallback
		out, err := exec.Command("uptime").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	return strings.TrimSpace(string(data)), nil
}

// diagLoadAvg returns load averages from /proc/loadavg.
func diagLoadAvg() (string, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// diagMemInfo returns summarized memory info.
func diagMemInfo() (string, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		// Try free command as fallback
		out, err := exec.Command("free", "-h").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	defer f.Close()

	var total, free, available string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = fields[1] + " kB"
		case "MemFree:":
			free = fields[1] + " kB"
		case "MemAvailable:":
			available = fields[1] + " kB"
		}
	}

	return fmt.Sprintf("Total: %s, Free: %s, Available: %s", total, free, available), nil
}

// diagDiskFree returns disk usage for cwd.
func diagDiskFree() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "/"
	}
	out, err := exec.Command("df", "-h", cwd).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// diagIPAddr returns network interface addresses.
func diagIPAddr() (string, error) {
	// Try ip command first
	out, err := exec.Command("ip", "-br", "addr").Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	// Fall back to hostname -I
	out, err = exec.Command("hostname", "-I").Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	return "", fmt.Errorf("no ip tool available")
}

// diagHostname returns the system hostname.
func diagHostname() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	return hostname, nil
}

// diagKernel returns the kernel version.
func diagKernel() (string, error) {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		out, err := exec.Command("uname", "-r").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	// Return just the first line, trimmed
	lines := strings.SplitN(string(data), "\n", 2)
	return strings.TrimSpace(lines[0]), nil
}

// StatFile returns stat info for a file path.
func StatFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: %d bytes, mode %s, modified %s",
		path, info.Size(), info.Mode(), info.ModTime().Format("2006-01-02 15:04:05")), nil
}
