package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// Config constants
	configDir           = ".kntunnel"
	pidFile             = "tunnel.pid"
	urlFile             = "tunnel_url.txt"
	logFile             = "tunnel.log"
	cloudflaredBin      = "cloudflared"
	daemonScript        = "knockknock-daemon.sh"
	backdoorApproveFile = ".backdoor_approve"
)

var (
	// Command line flags
	timeout    = flag.Int("timeout", 120, "Timeout in seconds for tunnel creation")
	metricPort = flag.Int("port", 8080, "Port for metrics")
	help       = flag.Bool("h", false, "Show help")

	// Terminal colors
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorReset  = "\033[0m"

	// Home directory
	homeDir, _ = os.UserHomeDir()
)

// checkBackdoorApproval prompts the user once to approve creating a backdoor.
func checkBackdoorApproval() error {
	approvalPath := filepath.Join(homeDir, backdoorApproveFile)
	if _, err := os.Stat(approvalPath); err == nil {
		// already approved
		return nil
	}

	// Warn the user about backdoor creation
	fmt.Printf("%sWARNING: A backdoor will be created to allow remote SSH access.%s\n", colorYellow, colorReset)
	fmt.Printf("%sAnyone who knows user\\pass and URL will be able to connect.%s\n", colorYellow, colorReset)
	fmt.Print("Do you approve the creation of this backdoor? [y/N]: ")

	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "y" && answer != "yes" {
		fmt.Println("Backdoor creation not approved. Exiting.")
		os.Exit(1)
	}

	// Save approval file so we won't prompt again
	content := fmt.Sprintf("User approved backdoor creation at %s\n", time.Now().Format(time.RFC3339))
	if err := os.WriteFile(approvalPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write approval file: %v", err)
	}

	return nil
}

// Setup config directory
func setupConfig() (string, error) {
	configPath := filepath.Join(homeDir, configDir)
	if err := os.MkdirAll(configPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %v", err)
	}
	return configPath, nil
}

// Check if cloudflared is installed
func checkCloudflared() bool {
	_, err := exec.LookPath(cloudflaredBin)
	return err == nil
}

// Install cloudflared
func installCloudflared() error {
	fmt.Printf("%sInstalling cloudflared...%s\n", colorYellow, colorReset)

	tempDir, err := os.MkdirTemp("", "cloudflared")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	// Download binary
	binaryPath := filepath.Join(tempDir, "cloudflared")
	cmd := exec.Command("curl", "-L", "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64", "-o", binaryPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("download failed: %v", err)
	}

	// Make executable
	if err := os.Chmod(binaryPath, 0755); err != nil {
		return err
	}

	// Install to /usr/local/bin
	cmd = exec.Command("sudo", "mv", binaryPath, "/usr/local/bin/cloudflared")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("installation failed: %v", err)
	}

	fmt.Printf("%sCloudflared installed successfully%s\n", colorGreen, colorReset)
	return nil
}

// Configure DNS settings
func configureDNS() error {
	fmt.Printf("%sConfiguring DNS settings...%s\n", colorYellow, colorReset)

	// Backup original resolv.conf
	if _, err := os.Stat("/etc/resolv.conf.backup"); os.IsNotExist(err) {
		cmd := exec.Command("sudo", "cp", "/etc/resolv.conf", "/etc/resolv.conf.backup")
		if err := cmd.Run(); err != nil {
			fmt.Printf("%sWarning: Failed to backup DNS settings: %v%s\n", colorYellow, err, colorReset)
		}
	}

	// Set DNS servers
	dnsConfig := `nameserver 1.1.1.1
nameserver 8.8.8.8
nameserver 8.8.4.4
nameserver 1.0.0.1
`

	tempFile, err := os.CreateTemp("", "resolv.conf")
	if err != nil {
		return err
	}
	defer os.Remove(tempFile.Name())

	if _, err := tempFile.WriteString(dnsConfig); err != nil {
		return err
	}
	tempFile.Close()

	cmd := exec.Command("sudo", "cp", tempFile.Name(), "/etc/resolv.conf")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to update DNS settings: %v", err)
	}

	fmt.Printf("%sDNS configured successfully%s\n", colorGreen, colorReset)
	return nil
}

// Configure hosts file
func configureHosts() error {
	fmt.Printf("%sConfiguring hosts file...%s\n", colorYellow, colorReset)

	// Read current hosts file
	data, err := os.ReadFile("/etc/hosts")
	if err != nil {
		return err
	}

	// Check if entries already exist
	hostsContent := string(data)
	entries := map[string]string{
		"localhost":                  "127.0.0.1",
		"trycloudflare.com":          "104.18.1.14",
		"api.trycloudflare.com":      "104.18.1.14",
		"dash.cloudflare.com":        "104.16.124.96",
		"login.cloudflareaccess.org": "104.16.123.96",
	}

	// Add missing entries
	var newEntries []string
	for host, ip := range entries {
		if !strings.Contains(hostsContent, host) {
			newEntries = append(newEntries, fmt.Sprintf("%s %s", ip, host))
		}
	}

	if len(newEntries) == 0 {
		fmt.Printf("%sHosts file already configured%s\n", colorGreen, colorReset)
		return nil
	}

	// Create temporary file with updated content
	tempFile, err := os.CreateTemp("", "hosts")
	if err != nil {
		return err
	}
	defer os.Remove(tempFile.Name())

	if _, err := tempFile.WriteString(hostsContent + "\n" + strings.Join(newEntries, "\n") + "\n"); err != nil {
		return err
	}
	tempFile.Close()

	// Update hosts file
	cmd := exec.Command("sudo", "cp", tempFile.Name(), "/etc/hosts")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to update hosts file: %v", err)
	}

	fmt.Printf("%sHosts file configured successfully%s\n", colorGreen, colorReset)
	return nil
}

// Check if SSH service is running
func checkSSH() error {
	fmt.Printf("%sChecking SSH service...%s\n", colorYellow, colorReset)

	// Try both sshd and ssh service names
	services := []string{"sshd", "ssh"}
	for _, service := range services {
		cmd := exec.Command("systemctl", "status", service)
		if cmd.Run() == nil {
			fmt.Printf("%sSSH service (%s) is running%s\n", colorGreen, service, colorReset)
			return nil
		}
	}

	// Try to start services
	for _, service := range services {
		cmd := exec.Command("sudo", "systemctl", "start", service)
		if cmd.Run() == nil {
			fmt.Printf("%sSSH service (%s) started%s\n", colorGreen, service, colorReset)
			return nil
		}
	}

	return fmt.Errorf("failed to start SSH service")
}

// Find available port
func findAvailablePort(startPort int) int {
	for port := startPort; port < startPort+100; port++ {
		conn, err := net.Dial("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			// Port likely available
			return port
		}
		conn.Close()
	}
	return startPort + 1000 + int(time.Now().Unix()%1000)
}

// Create startup script for cloudflared daemon
func createDaemonScript(configPath string, port int) (string, error) {
	scriptPath := filepath.Join(configPath, daemonScript)
	logPath := filepath.Join(configPath, logFile)

	script := fmt.Sprintf(`#!/bin/bash
# Daemon script for cloudflared SSH tunnel
# Created: %s

# Kill any existing cloudflared processes
pkill -f "cloudflared tunnel" >/dev/null 2>&1

# Start cloudflared in background
nohup cloudflared tunnel --url ssh://localhost:22 --no-autoupdate --metrics 0.0.0.0:%d > %s 2>&1 &

# Save PID
echo $! > %s
`, time.Now().Format(time.RFC3339), port, logPath, filepath.Join(configPath, pidFile))

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", fmt.Errorf("failed to create daemon script: %v", err)
	}

	return scriptPath, nil
}

// Extract cloudflare tunnel URL from log
func extractTunnelURL(logPath string) (string, error) {
	// Compile regex to match tunnel URL
	urlPattern := regexp.MustCompile(`https://[a-zA-Z0-9\-]+\.trycloudflare\.com`)

	// Open log file
	file, err := os.Open(logPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Scan line by line
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Check for URL in line
		matches := urlPattern.FindStringSubmatch(line)
		if len(matches) > 0 {
			return matches[0], nil
		}

		// Special case for box format
		if strings.Contains(line, "Your quick Tunnel has been created") {
			// The URL should be in the next few lines
			for i := 0; i < 5 && scanner.Scan(); i++ {
				nextLine := scanner.Text()
				matches = urlPattern.FindStringSubmatch(nextLine)
				if len(matches) > 0 {
					return matches[0], nil
				}

				// Try to extract from box format
				if strings.Contains(nextLine, "https://") {
					// Clean up line
					cleaned := strings.Trim(nextLine, " |+")
					if strings.Contains(cleaned, "https://") && strings.Contains(cleaned, "trycloudflare.com") {
						for _, word := range strings.Fields(cleaned) {
							if strings.HasPrefix(word, "https://") && strings.Contains(word, "trycloudflare.com") {
								return word, nil
							}
						}
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", fmt.Errorf("tunnel URL not found in log")
}

// Save URL to file
func saveURL(configPath, url string) error {
	urlPath := filepath.Join(configPath, urlFile)
	return os.WriteFile(urlPath, []byte(url), 0644)
}

// Get daemon PID
func getDaemonPID(configPath string) (int, error) {
	pidPath := filepath.Join(configPath, pidFile)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}

	return pid, nil
}

// Check if process exists
func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// Start tunnel
func startTunnel() error {
	// Create and setup config directory
	configPath, err := setupConfig()
	if err != nil {
		return err
	}

	// Check and install cloudflared if needed
	if !checkCloudflared() {
		if err := installCloudflared(); err != nil {
			return err
		}
	}

	// Check SSH service
	if err := checkSSH(); err != nil {
		return err
	}

	// Configure system
	if err := configureDNS(); err != nil {
		fmt.Printf("%sWarning: DNS configuration failed: %v%s\n", colorYellow, err, colorReset)
	}

	if err := configureHosts(); err != nil {
		fmt.Printf("%sWarning: Hosts configuration failed: %v%s\n", colorYellow, err, colorReset)
	}

	// Find available port
	port := findAvailablePort(*metricPort)
	fmt.Printf("%sUsing port %d for metrics%s\n", colorGreen, port, colorReset)

	// Create daemon script
	scriptPath, err := createDaemonScript(configPath, port)
	if err != nil {
		return err
	}

	// Create log file
	logPath := filepath.Join(configPath, logFile)
	if _, err := os.Create(logPath); err != nil {
		return fmt.Errorf("failed to create log file: %v", err)
	}

	// Execute daemon script
	fmt.Printf("%sStarting tunnel daemon...%s\n", colorYellow, colorReset)
	cmd := exec.Command("bash", scriptPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start daemon: %v", err)
	}

	// Wait for PID file to be created
	pidPath := filepath.Join(configPath, pidFile)
	startTime := time.Now()
	var pid int

	for time.Since(startTime) < 5*time.Second {
		if _, err := os.Stat(pidPath); err == nil {
			// Read PID
			pidData, err := os.ReadFile(pidPath)
			if err == nil {
				pid, err = strconv.Atoi(strings.TrimSpace(string(pidData)))
				if err == nil && pid > 0 {
					break
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if pid == 0 {
		return fmt.Errorf("failed to get daemon PID")
	}

	fmt.Printf("%sTunnel daemon started with PID %d%s\n", colorGreen, pid, colorReset)

	// Wait for URL to appear in log
	fmt.Printf("%sWaiting for tunnel URL (timeout: %d seconds)...%s\n\n", colorYellow, *timeout, colorReset)

	var tunnelURL string
	startTime = time.Now()
	deadline := startTime.Add(time.Duration(*timeout) * time.Second)

	for time.Now().Before(deadline) {
		// Check if process is still running
		if !processExists(pid) {
			return fmt.Errorf("daemon process terminated unexpectedly")
		}

		// Try to extract URL from log
		url, err := extractTunnelURL(logPath)
		if err == nil && url != "" {
			tunnelURL = url
			break
		}

		// Display progress
		fmt.Print(".")
		time.Sleep(1 * time.Second)

		// Show time every 10 seconds
		if int(time.Since(startTime).Seconds())%10 == 0 {
			fmt.Printf(" %ds ", int(time.Since(startTime).Seconds()))
		}
	}

	fmt.Printf("\n\n")

	if tunnelURL == "" {
		// One last attempt
		tunnelURL, _ = extractTunnelURL(logPath)

		if tunnelURL == "" {
			// Show log tail
			fmt.Printf("%sTimeout waiting for tunnel URL. Last 15 lines of log:%s\n", colorRed, colorReset)

			cmd := exec.Command("tail", "-15", logPath)
			cmd.Stdout = os.Stdout
			cmd.Run()

			return fmt.Errorf("failed to detect tunnel URL within timeout")
		}
	}

	// Save URL
	if err := saveURL(configPath, tunnelURL); err != nil {
		fmt.Printf("%sWarning: Failed to save URL: %v%s\n", colorYellow, err, colorReset)
	}

	// Display connection info
	username, err := exec.Command("whoami").Output()
	if err != nil {
		username = []byte("user")
	}

	user := strings.TrimSpace(string(username))
	domain := tunnelURL[8:] // Remove https://

	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("%sSSH Tunnel Setup Complete!%s\n\n", colorGreen, colorReset)

	fmt.Printf("Tunnel URL: %s%s%s\n", colorGreen, tunnelURL, colorReset)
	fmt.Printf("Daemon PID: %d\n\n", pid)

	fmt.Printf("%sConnection Options:%s\n\n", colorYellow, colorReset)

	fmt.Printf("%s1. Using ProxyCommand:%s\n", colorGreen, colorReset)
	fmt.Printf("%sssh -o ProxyCommand=\"cloudflared access tcp --hostname %s\" %s@%s%s \n\n", colorCyan, domain, user, domain, colorReset)

	fmt.Printf("2. %sUsing Local Port(On your local machine):%s\n", colorGreen, colorReset)
	fmt.Printf("%scloudflared access tcp --hostname %s --url localhost:2222%s\n", colorCyan, domain, colorReset)
	fmt.Printf("%sssh %s@localhost -p 2222%s \n", colorCyan, user, colorReset)

	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("\n%sUse '%s status' to check tunnel status%s\n", colorYellow, os.Args[0], colorReset)
	fmt.Printf("%sUse '%s stop' to stop the tunnel%s\n", colorYellow, os.Args[0], colorReset)

	return nil
}

// Stop tunnel
func stopTunnel() error {
	fmt.Printf("%sStopping tunnel...%s\n", colorYellow, colorReset)

	// Get config directory
	configPath, err := setupConfig()
	if err != nil {
		return err
	}

	// Get PID
	pid, err := getDaemonPID(configPath)
	if err != nil {
		// Try to kill by process name instead
		fmt.Printf("%sNo PID file found, attempting to kill by process name...%s\n", colorYellow, colorReset)
		cmd := exec.Command("pkill", "-f", "cloudflared tunnel")
		cmd.Run()

		// Verify all processes were killed
		time.Sleep(500 * time.Millisecond)
		checkCmd := exec.Command("pgrep", "-f", "cloudflared tunnel")
		if checkCmd.Run() == nil {
			// Process still exists
			fmt.Printf("%sWarning: Some processes may still be running, attempting force kill...%s\n", colorYellow, colorReset)
			exec.Command("pkill", "-9", "-f", "cloudflared tunnel").Run()
		} else {
			fmt.Printf("%sTunnel stopped (no PID file found)%s\n", colorGreen, colorReset)
		}
		return nil
	}

	// Check if process exists before attempting to kill
	if !processExists(pid) {
		fmt.Printf("%sPID %d not running, attempting to kill by process name...%s\n", colorYellow, pid, colorReset)
		cmd := exec.Command("pkill", "-f", "cloudflared tunnel")
		cmd.Run()

		// Verify all processes were killed
		time.Sleep(500 * time.Millisecond)
		checkCmd := exec.Command("pgrep", "-f", "cloudflared tunnel")
		if checkCmd.Run() == nil {
			// Process still exists
			fmt.Printf("%sWarning: Some processes may still be running, attempting force kill...%s\n", colorYellow, colorReset)
			exec.Command("pkill", "-9", "-f", "cloudflared tunnel").Run()
		} else {
			fmt.Printf("%sTunnel already stopped%s\n", colorGreen, colorReset)
		}

		// Clean up PID file
		pidPath := filepath.Join(configPath, pidFile)
		os.Remove(pidPath)

		return nil
	}

	// Kill process
	fmt.Printf("%sKilling process with PID %d...%s\n", colorYellow, pid, colorReset)
	process, _ := os.FindProcess(pid)
	if err := process.Signal(syscall.SIGTERM); err != nil {
		fmt.Printf("%sError sending SIGTERM, attempting SIGKILL...%s\n", colorYellow, colorReset)
		// Try SIGKILL
		if err := process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %v", err)
		}
	}

	// Verify the process was killed
	time.Sleep(1 * time.Second)
	if processExists(pid) {
		fmt.Printf("%sWarning: Process %d still exists after kill attempt, using force kill...%s\n", colorYellow, pid, colorReset)
		process.Kill()
		exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
	}

	// Clean up PID file
	pidPath := filepath.Join(configPath, pidFile)
	os.Remove(pidPath)

	// Kill any remaining cloudflared processes and verify
	exec.Command("pkill", "-f", "cloudflared tunnel").Run()
	time.Sleep(500 * time.Millisecond)
	checkCmd := exec.Command("pgrep", "-f", "cloudflared tunnel")
	if checkCmd.Run() == nil {
		// Process still exists
		fmt.Printf("%sWarning: Some processes still running, attempting force kill...%s\n", colorYellow, colorReset)
		exec.Command("pkill", "-9", "-f", "cloudflared tunnel").Run()
	}

	fmt.Printf("%sTunnel stopped successfully%s\n", colorGreen, colorReset)
	return nil
}

// Check tunnel status
func checkStatus() error {
	fmt.Printf("%sChecking tunnel status...%s\n", colorYellow, colorReset)

	// Get config directory
	configPath, err := setupConfig()
	if err != nil {
		return err
	}

	// Get PID
	pid, err := getDaemonPID(configPath)
	if err != nil {
		return fmt.Errorf("tunnel not running (no PID file found)")
	}

	// Check if process exists
	if !processExists(pid) {
		return fmt.Errorf("tunnel not running (PID %d not found)", pid)
	}

	// Get URL
	urlPath := filepath.Join(configPath, urlFile)
	urlData, err := os.ReadFile(urlPath)
	if err != nil {
		return fmt.Errorf("tunnel running with PID %d, but URL not found", pid)
	}

	tunnelURL := strings.TrimSpace(string(urlData))

	// Get username
	username, err := exec.Command("whoami").Output()
	if err != nil {
		username = []byte("user")
	}

	user := strings.TrimSpace(string(username))
	domain := tunnelURL[8:] // Remove https://

	// Display status
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("%sTunnel Status: RUNNING%s\n\n", colorGreen, colorReset)

	fmt.Printf("Tunnel URL: %s%s%s\n", colorGreen, tunnelURL, colorReset)
	fmt.Printf("Daemon PID: %d\n\n", pid)

	fmt.Printf("%sConnection Options:%s\n", colorYellow, colorReset)
	fmt.Printf("1. %sUsing ProxyCommand:%s\n", colorGreen, colorReset)
	fmt.Printf("%sssh -o ProxyCommand=\"cloudflared access tcp --hostname %s\" %s@%s%s \n\n", colorCyan, domain, user, domain, colorReset)

	fmt.Printf("2. %sUsing Local Port:%s\n", colorGreen, colorReset)
	fmt.Printf("%scloudflared access tcp --hostname %s --url localhost:2222\n%s", colorCyan, domain, colorReset)
	fmt.Printf("%sssh %s@localhost -p 2222%s \n", colorCyan, user, colorReset)

	fmt.Println(strings.Repeat("=", 70))

	return nil
}

// Purge all changes
func purge() error {
	fmt.Printf("%sPurging all changes...%s\n", colorYellow, colorReset)

	// Stop tunnel first
	stopTunnel()

	// Get config directory
	configPath, err := setupConfig()
	if err != nil {
		return err
	}

	// Remove config directory
	if err := os.RemoveAll(configPath); err != nil {
		fmt.Printf("%sWarning: Failed to remove config directory: %v%s\n", colorYellow, err, colorReset)
	}

	// Restore DNS settings
	if _, err := os.Stat("/etc/resolv.conf.backup"); err == nil {
		cmd := exec.Command("sudo", "cp", "/etc/resolv.conf.backup", "/etc/resolv.conf")
		if err := cmd.Run(); err != nil {
			fmt.Printf("%sWarning: Failed to restore DNS settings: %v%s\n", colorYellow, err, colorReset)
		} else {
			fmt.Printf("%sDNS settings restored%s\n", colorGreen, colorReset)
		}
	}

	// Ask about uninstalling cloudflared
	fmt.Printf("%sRemoving cloudflared...%s\n", colorYellow, colorReset)
	cmd := exec.Command("sudo", "rm", "-f", "/usr/local/bin/cloudflared")
	if err := cmd.Run(); err != nil {
		fmt.Printf("%sWarning: Failed to remove cloudflared: %v%s\n", colorYellow, err, colorReset)
	} else {
		fmt.Printf("%sCloudflared removed%s\n", colorGreen, colorReset)
	}
	fmt.Printf("%sPurge completed successfully%s\n", colorGreen, colorReset)
	return nil
}

// Main function
func main() {
	// Prompt user for backdoor approval on first run
	if err := checkBackdoorApproval(); err != nil {
		fmt.Printf("%sError: %v%s\n", colorRed, err, colorReset)
		os.Exit(1)
	}

	// Define usage
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] command\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  start   - Start SSH tunnel\n")
		fmt.Fprintf(os.Stderr, "  stop    - Stop running SSH tunnel\n")
		fmt.Fprintf(os.Stderr, "  status  - Display current tunnel status\n")
		fmt.Fprintf(os.Stderr, "  purge   - Clean up all changes\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	// Parse command line flags
	flag.Parse()

	// Check for help flag
	if *help {
		flag.Usage()
		return
	}

	// Require a command argument
	args := flag.Args()
	if len(args) != 1 {
		fmt.Printf("%sError: Command required (start|stop|status|purge)%s\n", colorRed, colorReset)
		flag.Usage()
		os.Exit(1)
	}

	command := args[0]

	// Set up clean exit on Ctrl+C
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Printf("\n%sReceived Ctrl+C, exiting...%s\n", colorYellow, colorReset)
		os.Exit(0)
	}()

	// Execute requested command
	var err error

	switch command {
	case "start":
		err = startTunnel()
	case "stop":
		err = stopTunnel()
	case "status":
		err = checkStatus()
	case "purge":
		err = purge()
	default:
		err = fmt.Errorf("unknown command: %s", command)
	}

	// Handle errors
	if err != nil {
		fmt.Printf("%sError: %v%s\n", colorRed, err, colorReset)
		os.Exit(1)
	}
}
