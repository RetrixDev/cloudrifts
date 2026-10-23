package provisioning

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type SwitchWebserverSpec struct {
	JobID    string        `json:"job_id"`
	From     string        `json:"from"`
	To       string        `json:"to"`
	Websites []WebsiteSpec `json:"websites"`
}

type switcher struct {
	ctx      context.Context
	spec     SwitchWebserverSpec
	dryRun   bool
	steps    []StepResult
	issues   []Issue
	logs     []string
	rollback []string
	undo     []func()
}

func ApplySwitchWebserver(ctx context.Context, spec SwitchWebserverSpec, dryRun bool) Result {
	sw := &switcher{ctx: ctx, spec: spec, dryRun: dryRun}
	sw.spec.From = normalizeWebserverID(sw.spec.From)
	sw.spec.To = normalizeWebserverID(sw.spec.To)
	sw.log("starting webserver switch from %s to %s for %d websites", emptyAs(sw.spec.From, "unknown"), sw.spec.To, len(sw.spec.Websites))

	if !sw.runStep("preflight", "Validate target webserver", "Check Ubuntu, root helper, target webserver installation, and website metadata before touching active traffic", sw.preflight) {
		return sw.finish(ResultFailed)
	}
	if !sw.runStep("render", "Render target configs", "Generate virtual hosts for every website on the target webserver while keeping the current engine untouched", sw.renderTargetConfigs) {
		sw.runRollback()
		return sw.finish(ResultFailed)
	}
	if !sw.runStep("validate", "Validate target config", "Run the target webserver config test before moving traffic", sw.validateTargetConfig) {
		sw.runRollback()
		return sw.finish(ResultFailed)
	}
	if !sw.runStep("activate", "Activate target webserver", "Stop conflicting listeners, start the selected engine, reload it, and restore the previous engine if activation fails", sw.activateTarget) {
		sw.runRollback()
		return sw.finish(ResultFailed)
	}
	return sw.finish(ResultDone)
}

func (sw *switcher) runStep(id, title, detail string, fn func() stepOutcome) bool {
	started := time.Now().UTC()
	step := StepResult{ID: id, Title: title, Detail: detail, Status: StepRunning, StartedAt: &started}
	sw.log("%s: %s", id, title)
	out := fn()
	ended := time.Now().UTC()
	step.EndedAt = &ended
	if out.Detail != "" {
		step.Detail = out.Detail
	}
	step.Issues = out.Issues
	if out.Err != nil {
		issue := Issue{Code: id + "_failed", Severity: "critical", Title: title + " failed", Detail: out.Err.Error(), Fix: switchRecoveryForStep(id, sw.spec), Retryable: true}
		step.Issues = append(step.Issues, issue)
		sw.issues = append(sw.issues, issue)
		step.Status = StepFailed
		sw.log("%s failed: %s", id, out.Err.Error())
		sw.steps = append(sw.steps, step)
		return false
	}
	if out.Status == "" {
		out.Status = StepDone
	}
	step.Status = out.Status
	if len(out.Issues) > 0 {
		sw.issues = append(sw.issues, out.Issues...)
	}
	if step.Status == StepWarning {
		sw.log("%s warning: %s", id, firstIssueFix(out.Issues))
	} else {
		sw.log("%s done", id)
	}
	sw.steps = append(sw.steps, step)
	return true
}

func (sw *switcher) preflight() stepOutcome {
	if sw.spec.To == "" {
		return stepOutcome{Err: errors.New("target webserver is required")}
	}
	if !validWebserverID(sw.spec.To) {
		return stepOutcome{Err: fmt.Errorf("unsupported webserver %q", sw.spec.To)}
	}
	if sw.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Dry run: %s target accepted; %d website configs will be generated", webserverDisplayName(sw.spec.To), len(sw.spec.Websites))}
	}
	if runtime.GOOS != "linux" {
		return stepOutcome{Err: fmt.Errorf("real webserver switching is supported on Ubuntu Linux only, current OS is %s", runtime.GOOS)}
	}
	if os.Geteuid() != 0 {
		return stepOutcome{Err: errors.New("cloudrifts-agent must run as root to switch webservers")}
	}
	if !isUbuntu2404() {
		return stepOutcome{Err: errors.New("Cloudrifts Phase 1 supports Ubuntu 24.04 only")}
	}
	probe := &applier{ctx: sw.ctx, spec: WebsiteSpec{Webserver: sw.spec.To}}
	if err := probe.validateActiveWebserverInstalled(); err != nil {
		return stepOutcome{Err: err}
	}
	for _, site := range sw.spec.Websites {
		if strings.TrimSpace(site.SiteID) == "" || strings.TrimSpace(site.Domain) == "" || strings.TrimSpace(site.DocumentRoot) == "" || strings.TrimSpace(site.HomeDir) == "" {
			return stepOutcome{Err: fmt.Errorf("website metadata is incomplete for %s", emptyAs(site.Domain, site.SiteID))}
		}
		if !strings.HasPrefix(filepath.Clean(site.DocumentRoot), filepath.Clean(site.HomeDir)+string(os.PathSeparator)) {
			return stepOutcome{Err: fmt.Errorf("document root %s must stay inside home %s", site.DocumentRoot, site.HomeDir)}
		}
	}
	return stepOutcome{Detail: fmt.Sprintf("%s is installed and %d website records are safe to render", webserverDisplayName(sw.spec.To), len(sw.spec.Websites))}
}

func (sw *switcher) renderTargetConfigs() stepOutcome {
	if sw.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Would render %d %s vhosts without changing live traffic", len(sw.spec.Websites), webserverDisplayName(sw.spec.To))}
	}
	for _, site := range sw.spec.Websites {
		site.Webserver = sw.spec.To
		if site.SystemGroup == "" {
			site.SystemGroup = siteGroup
		}
		if site.LogDir == "" {
			site.LogDir = filepath.Join(site.HomeDir, "logs")
		}
		if err := os.MkdirAll(site.LogDir, 0o755); err != nil {
			return stepOutcome{Err: err}
		}
		if err := sw.renderSite(site); err != nil {
			return stepOutcome{Err: err}
		}
	}
	if len(sw.spec.Websites) == 0 {
		return stepOutcome{Detail: "No websites exist yet; only the active webserver role will change"}
	}
	return stepOutcome{Detail: fmt.Sprintf("Rendered %d %s virtual hosts", len(sw.spec.Websites), webserverDisplayName(sw.spec.To))}
}

func (sw *switcher) renderSite(site WebsiteSpec) error {
	socket := phpSocketPath(safePoolName(site.SiteID))
	cert, key, hasCert := existingCertificate(site.Domain)
	switch sw.spec.To {
	case "nginx":
		content := nginxHTTPConfig(site, socket)
		if hasCert {
			content = nginxSSLConfig(site, socket, cert, key)
		}
		available := nginxAvailablePath(site.SiteID)
		enabled := nginxEnabledPath(site.SiteID)
		if err := os.MkdirAll(filepath.Dir(available), 0o755); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(enabled), 0o755); err != nil {
			return err
		}
		if err := sw.writeFileWithRollback(available, []byte(content), 0o644); err != nil {
			return err
		}
		oldLink, hadLink := readlinkIfExists(enabled)
		oldFile, hadFile := readIfExists(enabled)
		_ = os.Remove(enabled)
		if err := os.Symlink(available, enabled); err != nil {
			return err
		}
		sw.undo = append(sw.undo, func() {
			_ = os.Remove(enabled)
			if hadLink {
				_ = os.Symlink(oldLink, enabled)
			} else if hadFile {
				_ = os.WriteFile(enabled, oldFile, 0o644)
			}
		})
		sw.rollback = append(sw.rollback, "enabled Nginx vhost "+enabled)
	case "apache":
		content := apacheHTTPConfig(site, socket)
		if hasCert {
			content = apacheSSLConfig(site, socket, cert, key)
		}
		available := apacheAvailablePath(site.SiteID)
		if err := os.MkdirAll(filepath.Dir(available), 0o755); err != nil {
			return err
		}
		if err := sw.writeFileWithRollback(available, []byte(content), 0o644); err != nil {
			return err
		}
		for _, mod := range []string{"proxy", "proxy_fcgi", "setenvif", "rewrite", "headers", "ssl"} {
			if err := sw.run("a2enmod", mod); err != nil {
				return err
			}
		}
		if err := sw.run("a2ensite", filepath.Base(available)); err != nil {
			return err
		}
		sw.undo = append(sw.undo, func() { _ = exec.Command("a2dissite", filepath.Base(available)).Run() })
		sw.rollback = append(sw.rollback, "enabled Apache vhost "+available)
	case "openlitespeed", "litespeed":
		root := liteSpeedVHostRoot(site.SiteID)
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
		content := liteSpeedHTTPConfig(site, socket)
		if hasCert {
			content = liteSpeedSSLConfig(site, socket, cert, key)
		}
		conf := filepath.Join(root, "vhconf.conf")
		if err := sw.writeFileWithRollback(conf, []byte(content), 0o644); err != nil {
			return err
		}
		mapper := &applier{ctx: sw.ctx, spec: site}
		if err := mapper.ensureLiteSpeedMainMapping(webserverEngineFor(sw.spec.To)); err != nil {
			return err
		}
		sw.logs = append(sw.logs, mapper.logs...)
		sw.undo = append(sw.undo, mapper.created...)
		sw.rollback = append(sw.rollback, mapper.rollback...)
	default:
		return fmt.Errorf("unsupported webserver %q", sw.spec.To)
	}
	return nil
}

func (sw *switcher) validateTargetConfig() stepOutcome {
	if sw.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Would validate %s config before activation", webserverDisplayName(sw.spec.To))}
	}
	if err := sw.validateWebserver(sw.spec.To); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: webserverDisplayName(sw.spec.To) + " config validation passed"}
}

func (sw *switcher) activateTarget() stepOutcome {
	if sw.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Would activate %s, stop conflicting engines, and rollback automatically on failure", webserverDisplayName(sw.spec.To))}
	}
	previous := normalizeWebserverID(sw.spec.From)
	if previous == "" {
		previous = "nginx"
	}
	if previous == sw.spec.To {
		if err := sw.reloadWebserver(sw.spec.To); err != nil {
			return stepOutcome{Err: err}
		}
		return stepOutcome{Detail: webserverDisplayName(sw.spec.To) + " was already selected; config reloaded"}
	}
	if err := sw.stopConflictingWebservers(sw.spec.To, previous); err != nil {
		return stepOutcome{Err: err}
	}
	if err := sw.startWebserver(sw.spec.To); err != nil {
		sw.recoverPreviousWebserver(previous)
		return stepOutcome{Err: err}
	}
	if err := sw.reloadWebserver(sw.spec.To); err != nil {
		sw.recoverPreviousWebserver(previous)
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: fmt.Sprintf("Traffic moved from %s to %s", webserverDisplayName(previous), webserverDisplayName(sw.spec.To))}
}

func (sw *switcher) validateWebserver(id string) error {
	switch id {
	case "nginx":
		return sw.run("nginx", "-t")
	case "apache":
		return sw.run("apache2ctl", "configtest")
	case "openlitespeed", "litespeed":
		return sw.run("/usr/local/lsws/bin/lswsctrl", "status")
	default:
		return fmt.Errorf("unsupported webserver %q", id)
	}
}

func (sw *switcher) stopConflictingWebservers(target string, previous string) error {
	for _, service := range servicesForOtherWebservers(target) {
		sw.log("stopping conflicting service %s", service)
		if service == "lsws" {
			_ = exec.CommandContext(sw.ctx, "/usr/local/lsws/bin/lswsctrl", "stop").Run()
			continue
		}
		_ = exec.CommandContext(sw.ctx, "systemctl", "stop", service).Run()
	}
	sw.undo = append(sw.undo, func() { sw.recoverPreviousWebserver(previous) })
	sw.rollback = append(sw.rollback, "prepared rollback to "+webserverDisplayName(previous))
	return nil
}

func (sw *switcher) startWebserver(id string) error {
	switch id {
	case "nginx":
		if err := sw.run("systemctl", "enable", "nginx"); err != nil {
			return err
		}
		return sw.run("systemctl", "start", "nginx")
	case "apache":
		if err := sw.run("systemctl", "enable", "apache2"); err != nil {
			return err
		}
		return sw.run("systemctl", "start", "apache2")
	case "openlitespeed", "litespeed":
		return sw.run("/usr/local/lsws/bin/lswsctrl", "start")
	default:
		return fmt.Errorf("unsupported webserver %q", id)
	}
}

func (sw *switcher) reloadWebserver(id string) error {
	switch id {
	case "nginx":
		return sw.run("systemctl", "reload", "nginx")
	case "apache":
		return sw.run("systemctl", "reload", "apache2")
	case "openlitespeed", "litespeed":
		return sw.run("/usr/local/lsws/bin/lswsctrl", "restart")
	default:
		return fmt.Errorf("unsupported webserver %q", id)
	}
}

func (sw *switcher) recoverPreviousWebserver(previous string) {
	if previous == "" || previous == sw.spec.To {
		return
	}
	sw.log("recovering previous webserver %s", previous)
	_ = sw.startWebserver(previous)
	_ = sw.reloadWebserver(previous)
}

func servicesForOtherWebservers(target string) []string {
	target = normalizeWebserverID(target)
	services := []string{}
	if target != "nginx" {
		services = append(services, "nginx")
	}
	if target != "apache" {
		services = append(services, "apache2")
	}
	if target != "openlitespeed" && target != "litespeed" {
		services = append(services, "lsws")
	}
	return services
}

func (sw *switcher) writeFileWithRollback(path string, content []byte, mode os.FileMode) error {
	old, hadOld := readIfExists(path)
	if err := os.WriteFile(path, content, mode); err != nil {
		return err
	}
	sw.undo = append(sw.undo, func() {
		if hadOld {
			_ = os.WriteFile(path, old, mode)
		} else {
			_ = os.Remove(path)
		}
	})
	sw.rollback = append(sw.rollback, "wrote "+path)
	return nil
}

func (sw *switcher) runRollback() {
	if sw.dryRun || len(sw.undo) == 0 {
		return
	}
	sw.log("webserver switch rollback started")
	for i := len(sw.undo) - 1; i >= 0; i-- {
		sw.undo[i]()
	}
	sw.log("webserver switch rollback finished")
}

func (sw *switcher) finish(defaultStatus ResultStatus) Result {
	status := defaultStatus
	for _, step := range sw.steps {
		if step.Status == StepFailed {
			status = ResultFailed
			break
		}
		if step.Status == StepWarning && status != ResultFailed {
			status = ResultWarning
		}
	}
	sw.log("webserver switch finished with %s", status)
	return Result{Status: status, Steps: sw.steps, Issues: sw.issues, Logs: sw.logs, Rollback: sw.rollback, DryRun: sw.dryRun}
}

func (sw *switcher) run(name string, args ...string) error {
	cmd := exec.CommandContext(sw.ctx, name, args...)
	out, err := cmd.CombinedOutput()
	line := strings.TrimSpace(string(out))
	if line != "" {
		sw.log("%s %s → %s", name, strings.Join(args, " "), line)
	} else {
		sw.log("%s %s", name, strings.Join(args, " "))
	}
	if err != nil {
		if line == "" {
			line = err.Error()
		}
		return fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), line)
	}
	return nil
}

func (sw *switcher) log(format string, args ...any) {
	sw.logs = append(sw.logs, time.Now().UTC().Format(time.RFC3339)+" "+fmt.Sprintf(format, args...))
}

func existingCertificate(domain string) (string, string, bool) {
	cert := "/etc/letsencrypt/live/" + domain + "/fullchain.pem"
	key := "/etc/letsencrypt/live/" + domain + "/privkey.pem"
	if _, err := os.Stat(cert); err != nil {
		return cert, key, false
	}
	if _, err := os.Stat(key); err != nil {
		return cert, key, false
	}
	return cert, key, true
}

func readlinkIfExists(path string) (string, bool) {
	value, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	return value, true
}

func switchRecoveryForStep(id string, spec SwitchWebserverSpec) string {
	switch id {
	case "preflight":
		return "Install " + webserverDisplayName(spec.To) + " on Ubuntu 24.04 or select an installed webserver."
	case "render":
		return "Fix the exact file permission or config generation error shown in the job log, then retry the switch."
	case "validate":
		return "Fix the target webserver config error shown in the job log; Cloudrifts keeps the current webserver active."
	case "activate":
		return "Cloudrifts attempted to restore the previous webserver. Check the job log, then retry after the target service is healthy."
	default:
		return "Fix the reported issue and retry the webserver switch."
	}
}

func normalizeWebserverID(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "apache", "apache2":
		return "apache"
	case "openlitespeed", "ols":
		return "openlitespeed"
	case "litespeed", "lsws":
		return "litespeed"
	case "", "nginx":
		return "nginx"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func validWebserverID(value string) bool {
	switch normalizeWebserverID(value) {
	case "nginx", "apache", "openlitespeed", "litespeed":
		return true
	default:
		return false
	}
}

func emptyAs(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
