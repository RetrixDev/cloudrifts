package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cloudrifts/panel/internal/config"
	"github.com/cloudrifts/panel/internal/setupauth"
)

const version = "0.1.0-dev"

var domainPattern = regexp.MustCompile(`^(?i)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

type cli struct {
	name string
	cfg  config.Config
}

type healthResult struct {
	OK      bool   `json:"ok"`
	Service string `json:"service"`
	Error   string `json:"error,omitempty"`
	Status  string `json:"status,omitempty"`
}

type doctorCheck struct {
	Name   string
	Status string
	Detail string
}

func main() {
	app := cli{name: filepath.Base(os.Args[0]), cfg: config.LoadDefault()}
	if len(os.Args) < 2 {
		if app.isProductCommand() {
			app.hub(nil)
			return
		}
		app.usage(2)
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "setup-url":
		app.setupURL(args, true)
	case "sso", "login":
		app.setupURL(args, false)
	case "password", "reset-password":
		app.password(args)
	case "status":
		app.status(args)
	case "restart":
		app.restart(args)
	case "logs":
		app.logs(args)
	case "doctor":
		app.doctor(args)
	case "db-password", "database-password":
		app.rotateDBPassword(args)
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		app.usage(0)
	default:
		app.usage(2)
	}
}

func (app cli) isProductCommand() bool {
	return app.name == "cloudrifts"
}

func (app cli) setupURL(args []string, rawDefault bool) {
	fs := flag.NewFlagSet("sso", flag.ExitOnError)
	host := fs.String("host", "", "host or IP for the setup/login URL")
	port := fs.String("port", "8080", "panel HTTP port")
	dataDir := fs.String("data-dir", app.cfg.DataDir, "Cloudrifts data directory")
	ttl := fs.Duration("ttl", 24*time.Hour, "token lifetime")
	jsonOut := fs.Bool("json", false, "print JSON")
	plainOut := fs.Bool("plain", false, "print only the URL")
	_ = fs.Parse(args)

	resolvedHost := strings.TrimSpace(*host)
	if resolvedHost == "" {
		resolvedHost = firstHostGuess()
	}
	store := setupauth.NewStore(*dataDir)
	token, err := store.Create(*ttl)
	if err != nil {
		app.fatal(err)
	}
	url := fmt.Sprintf("http://%s:%s/setup/sso/%s", resolvedHost, strings.TrimPrefix(*port, ":"), token.Token)
	_ = writeLastSetupURL(*dataDir, url)

	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"url": url, "expires_at": token.ExpiresAt})
		return
	}
	if rawDefault || *plainOut || !isTTY(os.Stdout) {
		fmt.Println(url)
		return
	}
	app.renderSSOCard(url, token.ExpiresAt)
}

func (app cli) password(args []string) {
	fs := flag.NewFlagSet("password", flag.ExitOnError)
	host := fs.String("host", "", "host or IP for the login URL")
	port := fs.String("port", "8080", "panel HTTP port")
	dataDir := fs.String("data-dir", app.cfg.DataDir, "Cloudrifts data directory")
	_ = fs.Parse(args)
	app.setupURL([]string{"--host", *host, "--port", *port, "--data-dir", *dataDir}, false)
}

func (app cli) status(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	addr := fs.String("addr", "http://127.0.0.1:8080", "cloudriftsd base URL")
	jsonOut := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)

	health := app.health(*addr)
	service := systemctlOutput("is-active", "cloudriftsd")
	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"health":  health,
			"service": strings.TrimSpace(service),
			"version": version,
		})
		return
	}
	if !app.isProductCommand() || !isTTY(os.Stdout) {
		if health.OK {
			fmt.Println("cloudriftsd is healthy")
			return
		}
		app.fatal(errors.New(health.Error))
	}
	app.renderStatusCard(*addr, health, strings.TrimSpace(service))
}

func (app cli) restart(args []string) {
	fs := flag.NewFlagSet("restart", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)

	start := time.Now()
	err := runPrivileged("systemctl", "restart", "cloudriftsd")
	ready := false
	if err == nil {
		ready = app.waitForHealth("http://127.0.0.1:8080", 10*time.Second)
		if !ready {
			err = errors.New("cloudriftsd restarted but API did not become ready within 10s")
		}
	}
	result := map[string]any{"ok": err == nil, "ready": ready, "duration_ms": time.Since(start).Milliseconds()}
	if err != nil {
		result["error"] = err.Error()
	}
	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(result)
		if err != nil {
			os.Exit(1)
		}
		return
	}
	if err != nil {
		app.fatal(err)
	}
	if app.isProductCommand() && isTTY(os.Stdout) {
		app.renderSimpleCard("Restart complete", "cloudriftsd restarted and API is ready", []string{"Systemd is active", "Health endpoint answered successfully"}, cMint)
		return
	}
	fmt.Println("cloudriftsd restarted")
}

func (app cli) logs(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	lines := fs.Int("lines", 80, "number of journal lines")
	follow := fs.Bool("follow", false, "follow logs")
	_ = fs.Parse(args)
	journalArgs := []string{"-u", "cloudriftsd", "-n", strconv.Itoa(*lines), "--no-pager"}
	if *follow {
		journalArgs = append(journalArgs, "-f")
	}
	cmd, err := privilegedCommand("journalctl", journalArgs...)
	if err != nil {
		app.fatal(err)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		app.fatal(err)
	}
}

func (app cli) doctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	addr := fs.String("addr", "http://127.0.0.1:8080", "cloudriftsd base URL")
	jsonOut := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)

	checks := app.doctorChecks(*addr)
	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"checks": checks})
		return
	}
	if !app.isProductCommand() || !isTTY(os.Stdout) {
		failed := false
		for _, check := range checks {
			fmt.Printf("%-18s %-8s %s\n", check.Name, check.Status, check.Detail)
			if check.Status == "fail" {
				failed = true
			}
		}
		if failed {
			os.Exit(1)
		}
		return
	}
	app.renderDoctorCard(checks)
}

func (app cli) rotateDBPassword(args []string) {
	fs := flag.NewFlagSet("db-password", flag.ExitOnError)
	siteID := fs.String("site", "", "website id or domain")
	dataDir := fs.String("data-dir", app.cfg.DataDir, "Cloudrifts data directory")
	show := fs.Bool("show", true, "print the new password")
	jsonOut := fs.Bool("json", false, "print JSON")
	_ = fs.Parse(args)
	if *siteID == "" && fs.NArg() > 0 {
		*siteID = fs.Arg(0)
	}
	if strings.TrimSpace(*siteID) == "" {
		app.fatal(errors.New("usage: cloudrifts db-password --site example.com"))
	}
	res, err := rotateWebsiteDBPassword(*dataDir, strings.TrimSpace(*siteID))
	if *jsonOut {
		if err != nil {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": false, "error": err.Error()})
			os.Exit(1)
		}
		if !*show {
			res.NewPassword = ""
		}
		_ = json.NewEncoder(os.Stdout).Encode(res)
		return
	}
	if err != nil {
		app.fatal(err)
	}
	lines := []string{
		"MariaDB user rotated: " + res.DatabaseUser,
		"Panel state updated for " + res.Domain,
	}
	if res.WPConfigUpdated {
		lines = append(lines, "WordPress config updated")
	} else if res.WPConfigNote != "" {
		lines = append(lines, res.WPConfigNote)
	}
	if *show {
		lines = append(lines, "New password: "+res.NewPassword)
	}
	if app.isProductCommand() && isTTY(os.Stdout) {
		app.renderSimpleCard("Database password changed", res.Domain, lines, cMint)
		return
	}
	for _, line := range lines {
		fmt.Println(line)
	}
}

func (app cli) hub(args []string) {
	health := app.health("http://127.0.0.1:8080")
	active := strings.TrimSpace(systemctlOutput("is-active", "cloudriftsd"))
	setupURL := readLastSetupURL(app.cfg.DataDir)
	if !isTTY(os.Stdout) {
		fmt.Printf("Cloudrifts %s\nservice: %s\napi: %s\n", version, valueOr(active, "unknown"), okLabel(health.OK))
		if setupURL != "" {
			fmt.Println("sso: cloudrifts sso")
		}
		fmt.Println("commands: status, sso, restart, logs, doctor, db-password")
		return
	}
	app.renderHub(health, active, setupURL)
}

func (app cli) doctorChecks(addr string) []doctorCheck {
	health := app.health(addr)
	checks := []doctorCheck{
		checkFromBool("service", strings.TrimSpace(systemctlOutput("is-active", "cloudriftsd")) == "active", strings.TrimSpace(systemctlOutput("is-active", "cloudriftsd"))),
		checkFromBool("api", health.OK, valueOr(health.Error, "healthy")),
		checkFromBool("port 8080", tcpOK("127.0.0.1:8080", 1500*time.Millisecond), "local API listener"),
		checkPath("cloudrifts", "/usr/local/bin/cloudrifts", true),
		checkPath("cloudriftsctl", "/usr/local/bin/cloudriftsctl", true),
		checkPath("daemon", "/usr/local/bin/cloudriftsd", true),
		checkPath("agent", "/usr/local/bin/cloudrifts-agent", true),
		checkPath("filed", "/usr/local/bin/cloudrifts-filed", true),
		checkPath("data dir", app.cfg.DataDir, false),
		checkPath("web assets", filepath.Join(app.cfg.WebDir, "index.html"), false),
		checkPath("setup token", filepath.Join(app.cfg.DataDir, "setup-token.json"), false),
	}
	if runtime.GOOS == "linux" {
		checks = append(checks, checkCommand("nginx"), checkCommand("php"), checkCommand("mysql"))
	}
	return checks
}

func (app cli) waitForHealth(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if app.health(addr).OK {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

func (app cli) health(addr string) healthResult {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(addr, "/")+"/api/health", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return healthResult{OK: false, Error: err.Error()}
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return healthResult{OK: false, Status: res.Status, Error: strings.TrimSpace(string(body))}
	}
	var out healthResult
	if err := json.Unmarshal(body, &out); err != nil {
		return healthResult{OK: true, Service: "cloudriftsd"}
	}
	out.OK = true
	return out
}

func firstHostGuess() string {
	if value := os.Getenv("CLOUDRIFTS_PUBLIC_HOST"); value != "" {
		return value
	}
	if ip := awsPublicIP(); ip != "" {
		return ip
	}
	if ip := publicIP(); ip != "" {
		return ip
	}
	if ip := routeIP(); ip != "" {
		return ip
	}
	return "127.0.0.1"
}

func awsPublicIP() string {
	client := http.Client{Timeout: 1200 * time.Millisecond}
	req, _ := http.NewRequest(http.MethodPut, "http://169.254.169.254/latest/api/token", nil)
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "60")
	res, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	tokenBytes, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return ""
	}
	req, _ = http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data/public-ipv4", nil)
	req.Header.Set("X-aws-ec2-metadata-token", token)
	res, err = client.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 128))
	ip := strings.TrimSpace(string(b))
	if net.ParseIP(ip) != nil {
		return ip
	}
	return ""
}

func publicIP() string {
	client := http.Client{Timeout: 1800 * time.Millisecond}
	res, err := client.Get("https://api.ipify.org")
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 128))
	ip := strings.TrimSpace(string(b))
	if net.ParseIP(ip) != nil {
		return ip
	}
	return ""
}

func routeIP() string {
	cmd := exec.Command("sh", "-c", "ip -4 route get 1.1.1.1 2>/dev/null | awk '/src/ {for (i=1;i<=NF;i++) if ($i==\"src\") {print $(i+1); exit}}'")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	ip := strings.TrimSpace(string(out))
	if net.ParseIP(ip) != nil {
		return ip
	}
	return ""
}

func writeLastSetupURL(dataDir, url string) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, "last-setup-url.txt"), []byte(url+"\n"), 0o640)
}

func readLastSetupURL(dataDir string) string {
	b, err := os.ReadFile(filepath.Join(dataDir, "last-setup-url.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func systemctlOutput(args ...string) string {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil && len(out) == 0 {
		return "unknown"
	}
	return string(out)
}

func privilegedCommand(name string, args ...string) (*exec.Cmd, error) {
	if os.Geteuid() == 0 || name == "journalctl" {
		return exec.Command(name, args...), nil
	}
	if _, err := exec.LookPath("sudo"); err == nil {
		return exec.Command("sudo", append([]string{name}, args...)...), nil
	}
	return nil, errors.New("root privileges required")
}

func runPrivileged(name string, args ...string) error {
	cmd, err := privilegedCommand(name, args...)
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

func tcpOK(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func checkFromBool(name string, ok bool, detail string) doctorCheck {
	if ok {
		return doctorCheck{Name: name, Status: "pass", Detail: valueOr(detail, "ready")}
	}
	return doctorCheck{Name: name, Status: "fail", Detail: valueOr(detail, "not ready")}
}

func checkPath(name, path string, executable bool) doctorCheck {
	st, err := os.Stat(path)
	if err != nil {
		return doctorCheck{Name: name, Status: "fail", Detail: path + " missing"}
	}
	if executable && st.Mode()&0o111 == 0 {
		return doctorCheck{Name: name, Status: "fail", Detail: path + " is not executable"}
	}
	return doctorCheck{Name: name, Status: "pass", Detail: path}
}

func checkCommand(name string) doctorCheck {
	path, err := exec.LookPath(name)
	if err != nil {
		return doctorCheck{Name: name, Status: "warn", Detail: "not installed or not in PATH"}
	}
	return doctorCheck{Name: name, Status: "pass", Detail: path}
}

func okLabel(ok bool) string {
	if ok {
		return "healthy"
	}
	return "offline"
}

func valueOr(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func isTTY(file *os.File) bool {
	st, err := file.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func (app cli) fatal(err error) {
	prefix := app.name
	if prefix == "" {
		prefix = "cloudrifts"
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n", prefix, err)
	os.Exit(1)
}

func (app cli) usage(code int) {
	name := app.name
	if name == "" || name == "cloudriftsctl" {
		name = "cloudrifts"
	}
	fmt.Fprintf(os.Stderr, `%s %s

Usage:
  %s                    Show terminal command center
  %s sso                Create setup/login SSO URL
  %s status             Show service + API status
  %s restart            Restart cloudriftsd
  %s logs [--lines 80]  Show service logs
  %s doctor             Run install health checks
  %s db-password --site example.com
  %s version

Compatibility:
  cloudriftsctl setup-url [--host IP_OR_HOST] [--port 8080]
  cloudriftsctl status [--addr http://127.0.0.1:8080]
`, name, version, name, name, name, name, name, name, name, name)
	os.Exit(code)
}

// Terminal UI.

const (
	cReset  = "\033[0m"
	cBold   = "\033[1m"
	cDim    = "\033[2m"
	cViolet = "\033[38;2;132;110;255m"
	cCyan   = "\033[38;2;70;205;228m"
	cMint   = "\033[38;2;78;220;153m"
	cAmber  = "\033[38;2;235;181;84m"
	cRose   = "\033[38;2;240;92;120m"
	cMuted  = "\033[38;2;132;146;166m"
	cLine   = "\033[38;2;58;70;94m"
)

func (app cli) renderHub(health healthResult, active, setupURL string) {
	apiTone := cRose
	apiText := "offline"
	if health.OK {
		apiTone = cMint
		apiText = "healthy"
	}
	serviceTone := cAmber
	if active == "active" {
		serviceTone = cMint
	}
	setupText := "run cloudrifts sso"
	if setupURL != "" {
		setupText = "ready · cloudrifts sso refreshes it"
	}
	boxTop()
	boxLine(fmt.Sprintf("  %s●%s %s●%s %s●%s  %scloudrifts%s command center                         %s%s%s", cRose, cReset, cAmber, cReset, cMint, cReset, cMuted, cReset, cDim, version, cReset))
	boxSep()
	boxLine(fmt.Sprintf("  %sCloudrifts%s telemetry hub", cBold, cReset))
	boxLine(fmt.Sprintf("  %sAPI%s %s● %s%s        %sService%s %s● %s%s", cMuted, cReset, apiTone, apiText, cReset, cMuted, cReset, serviceTone, valueOr(active, "unknown"), cReset))
	boxLine("")
	boxLine(fmt.Sprintf("  %sLogin%s      cloudrifts sso            %s%s%s", cCyan, cReset, cMuted, setupText, cReset))
	boxLine(fmt.Sprintf("  %sOperate%s    cloudrifts restart        cloudrifts logs --lines 120", cViolet, cReset))
	boxLine(fmt.Sprintf("  %sInspect%s    cloudrifts status         cloudrifts doctor", cMint, cReset))
	boxLine(fmt.Sprintf("  %sSecrets%s    cloudrifts db-password --site example.com", cAmber, cReset))
	boxLine("")
	boxLine(fmt.Sprintf("  %sProduct rule:%s one server command center; no file hunting for normal actions.", cMuted, cReset))
	boxBottom()
}

func (app cli) renderSSOCard(url string, expires time.Time) {
	boxTop()
	boxLine(fmt.Sprintf("  %scloudrifts%s secure handoff", cBold, cReset))
	boxSep()
	boxLine(fmt.Sprintf("  %sLogin link%s", cCyan, cReset))
	for _, part := range wrapPlain(url, 70) {
		boxLine("  " + part)
	}
	boxLine("")
	boxLine(fmt.Sprintf("  %sExpires%s %s", cMuted, cReset, expires.Local().Format("2006-01-02 15:04 MST")))
	boxLine(fmt.Sprintf("  %sUse this for first-run setup or admin recovery.%s", cMuted, cReset))
	boxBottom()
}

func (app cli) renderStatusCard(addr string, health healthResult, active string) {
	apiTone := cRose
	apiText := valueOr(health.Error, "offline")
	if health.OK {
		apiTone = cMint
		apiText = "healthy"
	}
	serviceTone := cAmber
	if active == "active" {
		serviceTone = cMint
	}
	boxTop()
	boxLine(fmt.Sprintf("  %scloudrifts%s status", cBold, cReset))
	boxSep()
	boxLine(fmt.Sprintf("  API      %s● %s%s   %s", apiTone, trim(apiText, 46), cReset, addr))
	boxLine(fmt.Sprintf("  Service  %s● %s%s", serviceTone, valueOr(active, "unknown"), cReset))
	boxLine(fmt.Sprintf("  Version  %s", version))
	boxLine("")
	boxLine(fmt.Sprintf("  %sNext%s cloudrifts doctor · cloudrifts logs --lines 120", cMuted, cReset))
	boxBottom()
}

func (app cli) renderDoctorCard(checks []doctorCheck) {
	boxTop()
	boxLine(fmt.Sprintf("  %scloudrifts%s doctor", cBold, cReset))
	boxSep()
	failed := 0
	for _, check := range checks {
		color := cMint
		mark := "✓"
		if check.Status == "warn" {
			color = cAmber
			mark = "!"
		}
		if check.Status == "fail" {
			color = cRose
			mark = "×"
			failed++
		}
		boxLine(fmt.Sprintf("  %s%s%s %-14s %s", color, mark, cReset, check.Name, trim(check.Detail, 44)))
	}
	boxLine("")
	if failed == 0 {
		boxLine(fmt.Sprintf("  %sAll critical checks passed.%s", cMint, cReset))
	} else {
		boxLine(fmt.Sprintf("  %s%d check(s) need attention.%s Run cloudrifts logs.", cRose, failed, cReset))
	}
	boxBottom()
	if failed > 0 {
		os.Exit(1)
	}
}

func (app cli) renderSimpleCard(title, detail string, lines []string, color string) {
	boxTop()
	boxLine(fmt.Sprintf("  %s%s%s", color, title, cReset))
	boxSep()
	boxLine("  " + detail)
	boxLine("")
	for _, line := range lines {
		boxLine("  " + line)
	}
	boxBottom()
}

func boxTop()    { fmt.Printf("%s╭%s╮%s\n", cLine, strings.Repeat("─", 76), cReset) }
func boxSep()    { fmt.Printf("%s├%s┤%s\n", cLine, strings.Repeat("─", 76), cReset) }
func boxBottom() { fmt.Printf("%s╰%s╯%s\n", cLine, strings.Repeat("─", 76), cReset) }

func boxLine(content string) {
	width := 76
	pad := width - ansiLen(content)
	if pad < 0 {
		pad = 0
	}
	fmt.Printf("%s│%s%s%s│%s\n", cLine, cReset, content, strings.Repeat(" ", pad), cReset)
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func ansiLen(s string) int {
	return len([]rune(ansiRE.ReplaceAllString(s, "")))
}

func wrapPlain(s string, max int) []string {
	runes := []rune(s)
	if len(runes) == 0 {
		return []string{""}
	}
	var out []string
	for len(runes) > max {
		out = append(out, string(runes[:max]))
		runes = runes[max:]
	}
	out = append(out, string(runes))
	return out
}

func trim(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// Website DB password rotation.

type rotateDBPasswordResult struct {
	OK              bool   `json:"ok"`
	Domain          string `json:"domain"`
	WebsiteID       string `json:"website_id"`
	DatabaseName    string `json:"database_name"`
	DatabaseUser    string `json:"database_user"`
	NewPassword     string `json:"new_password,omitempty"`
	WPConfigUpdated bool   `json:"wp_config_updated"`
	WPConfigNote    string `json:"wp_config_note,omitempty"`
}

func rotateWebsiteDBPassword(dataDir, siteRef string) (rotateDBPasswordResult, error) {
	statePath := filepath.Join(dataDir, "panel-state.json")
	b, err := os.ReadFile(statePath)
	if err != nil {
		return rotateDBPasswordResult{}, err
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return rotateDBPasswordResult{}, err
	}
	websites, _ := root["websites"].([]any)
	if len(websites) == 0 {
		return rotateDBPasswordResult{}, errors.New("no websites found in panel state")
	}
	var site map[string]any
	for _, item := range websites {
		candidate, _ := item.(map[string]any)
		if candidate == nil {
			continue
		}
		id := stringField(candidate, "id")
		domain := stringField(candidate, "domain")
		if strings.EqualFold(id, siteRef) || strings.EqualFold(domain, siteRef) {
			site = candidate
			break
		}
	}
	if site == nil {
		return rotateDBPasswordResult{}, fmt.Errorf("website %q not found", siteRef)
	}
	dbName := stringField(site, "database_name")
	dbUser := stringField(site, "database_user")
	if dbName == "" || dbUser == "" {
		return rotateDBPasswordResult{}, errors.New("this website does not have a managed database")
	}
	if !safeSQLIdent(dbName) || !safeSQLIdent(dbUser) {
		return rotateDBPasswordResult{}, errors.New("stored database identifiers are unsafe; refusing rotation")
	}
	newPassword := token(18)
	if err := alterMariaDBPassword(dbUser, newPassword); err != nil {
		return rotateDBPasswordResult{}, err
	}
	site["database_password"] = newPassword
	root["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return rotateDBPasswordResult{}, err
	}
	if err := os.WriteFile(statePath, append(out, '\n'), 0o640); err != nil {
		return rotateDBPasswordResult{}, err
	}
	res := rotateDBPasswordResult{OK: true, Domain: stringField(site, "domain"), WebsiteID: stringField(site, "id"), DatabaseName: dbName, DatabaseUser: dbUser, NewPassword: newPassword}
	updated, note := updateWPConfigPassword(site, newPassword)
	res.WPConfigUpdated = updated
	res.WPConfigNote = note
	return res, nil
}

func stringField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func safeSQLIdent(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for _, r := range v {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func alterMariaDBPassword(user, password string) error {
	sql := fmt.Sprintf("ALTER USER '%s'@'localhost' IDENTIFIED BY '%s'; FLUSH PRIVILEGES;", escapeSQL(user), escapeSQL(password))
	cmd := exec.Command("mysql", "-e", sql)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mysql password rotation failed: %w %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func updateWPConfigPassword(site map[string]any, password string) (bool, string) {
	docroot := stringField(site, "document_root")
	if docroot == "" {
		return false, "WordPress config skipped: no document root"
	}
	if _, err := os.Stat(filepath.Join(docroot, "wp-config.php")); err != nil {
		return false, "WordPress config skipped: wp-config.php not found"
	}
	wp, err := exec.LookPath("wp")
	if err != nil {
		return false, "WordPress config skipped: wp-cli not installed"
	}
	args := []string{"config", "set", "DB_PASSWORD", password, "--path=" + docroot, "--type=constant"}
	user := stringField(site, "system_user")
	var cmd *exec.Cmd
	if user != "" && os.Geteuid() == 0 {
		cmd = exec.Command("runuser", "-u", user, "--", wp)
		cmd.Args = append(cmd.Args, args...)
	} else {
		cmd = exec.Command(wp, args...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return false, "WordPress config update failed: " + strings.TrimSpace(stderr.String())
	}
	return true, ""
}

func escapeSQL(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func token(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}

func init() {
	_ = domainPattern
}
