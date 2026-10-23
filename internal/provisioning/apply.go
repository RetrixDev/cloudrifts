package provisioning

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const siteGroup = "cloudrifts-sites"

type applier struct {
	ctx             context.Context
	spec            WebsiteSpec
	dryRun          bool
	steps           []StepResult
	issues          []Issue
	logs            []string
	rollback        []string
	created         []func()
	sslIssued       bool
	databaseCreated bool
	appInstalled    bool
	certPath        string
	php             phpRuntime
}

type phpRuntime struct {
	Version string
	Service string
	PoolDir string
	FPMBin  string
}

type stepOutcome struct {
	Detail string
	Status StepStatus
	Issues []Issue
	Err    error
}

func ApplyCreateWebsite(ctx context.Context, spec WebsiteSpec, dryRun bool) Result {
	if spec.SystemGroup == "" {
		spec.SystemGroup = siteGroup
	}
	app := &applier{ctx: ctx, spec: spec, dryRun: dryRun}
	app.log("starting website provision for %s", spec.Domain)

	if !app.runStep("validate", "Validate server and request", "Check Ubuntu, root helper, active webserver, PHP-FPM, system account names, and document paths", app.validateEnvironment) {
		return app.finish(ResultFailed)
	}
	if !app.runStep("account", "Create isolated system account", "Create Linux user, group, home, document root, private temp directory, logs, and starter index", app.ensureAccount) {
		app.runRollback()
		return app.finish(ResultFailed)
	}
	if !app.runStep("database", "Prepare database", "Create MariaDB database, user, password, grants, and app credentials when the website needs WordPress", app.ensureDatabase) {
		app.runRollback()
		return app.finish(ResultFailed)
	}
	if !app.runStep("app", "Install application", "Install WordPress core and activate WooCommerce when selected", app.ensureApplication) {
		app.runRollback()
		return app.finish(ResultFailed)
	}
	if !app.runStep("runtime", "Prepare PHP-FPM pool", "Create a per-site PHP-FPM pool and validate PHP-FPM configuration", app.ensurePHPPool) {
		app.runRollback()
		return app.finish(ResultFailed)
	}
	if !app.runStep("vhost", "Generate webserver virtual host", "Write the HTTP vhost, ACME challenge route, PHP mapping, logs, and validate active webserver config", app.ensureWebserverHTTP) {
		app.runRollback()
		return app.finish(ResultFailed)
	}
	if !app.runStep("reload", "Reload services", "Reload PHP-FPM and active webserver only after both configs validate", app.reloadServices) {
		app.runRollback()
		return app.finish(ResultFailed)
	}
	app.runStep("ssl", "Issue SSL certificate", "Use DNS preflight, HTTP-01 webroot challenge, Let's Encrypt, and active webserver SSL vhost rewrite", app.ensureSSL)
	return app.finish(ResultDone)
}

func (a *applier) runStep(id, title, detail string, fn func() stepOutcome) bool {
	started := time.Now().UTC()
	step := StepResult{ID: id, Title: title, Detail: detail, Status: StepRunning, StartedAt: &started}
	a.log("%s: %s", id, title)
	out := fn()
	ended := time.Now().UTC()
	step.EndedAt = &ended
	if out.Detail != "" {
		step.Detail = out.Detail
	}
	step.Issues = out.Issues
	if out.Err != nil {
		issue := Issue{Code: id + "_failed", Severity: "critical", Title: title + " failed", Detail: out.Err.Error(), Fix: recoveryForStep(id, a.spec), Retryable: true}
		step.Issues = append(step.Issues, issue)
		a.issues = append(a.issues, issue)
		step.Status = StepFailed
		a.log("%s failed: %s", id, out.Err.Error())
		a.steps = append(a.steps, step)
		return false
	}
	if out.Status == "" {
		out.Status = StepDone
	}
	step.Status = out.Status
	if len(out.Issues) > 0 {
		a.issues = append(a.issues, out.Issues...)
	}
	if step.Status == StepWarning {
		a.log("%s warning: %s", id, firstIssueFix(out.Issues))
	} else {
		a.log("%s done", id)
	}
	a.steps = append(a.steps, step)
	return true
}

func (a *applier) validateEnvironment() stepOutcome {
	if a.spec.Domain == "" || a.spec.SiteID == "" || a.spec.DocumentRoot == "" || a.spec.SystemUser == "" {
		return stepOutcome{Err: errors.New("website spec is incomplete")}
	}
	if !strings.HasPrefix(filepath.Clean(a.spec.DocumentRoot), filepath.Clean(a.spec.HomeDir)+string(os.PathSeparator)) {
		return stepOutcome{Err: fmt.Errorf("document root %s must stay inside home %s", a.spec.DocumentRoot, a.spec.HomeDir)}
	}
	if a.dryRun {
		a.php = phpRuntime{Version: normalizePHPVersion(a.spec.PHPVersion), Service: "php" + normalizePHPVersion(a.spec.PHPVersion) + "-fpm", PoolDir: "/etc/php/" + normalizePHPVersion(a.spec.PHPVersion) + "/fpm/pool.d", FPMBin: "php-fpm" + normalizePHPVersion(a.spec.PHPVersion)}
		return stepOutcome{Detail: "Dry run on " + runtime.GOOS + ": validated generated user, paths, " + a.activeWebserver().Name + " vhost, PHP-FPM pool, and SSL decisions without changing the host."}
	}
	if runtime.GOOS != "linux" {
		return stepOutcome{Err: fmt.Errorf("real provisioning is supported on Ubuntu Linux only, current OS is %s", runtime.GOOS)}
	}
	if os.Geteuid() != 0 {
		return stepOutcome{Err: errors.New("cloudrifts-agent must run as root for user, PHP-FPM, Nginx, and certificate changes")}
	}
	if !isUbuntu2404() {
		return stepOutcome{Err: errors.New("Cloudrifts Phase 1 supports Ubuntu 24.04 only")}
	}
	for _, cmd := range []string{"systemctl", "useradd", "groupadd", "id"} {
		if _, err := exec.LookPath(cmd); err != nil {
			return stepOutcome{Err: fmt.Errorf("missing required command %s; run the Cloudrifts installer again", cmd)}
		}
	}
	if needsDatabase(a.spec.App) {
		if _, err := exec.LookPath("mysql"); err != nil {
			return stepOutcome{Err: errors.New("mysql client is missing; rerun the Cloudrifts installer so MariaDB tooling is installed")}
		}
	}
	if needsWordPress(a.spec.App) {
		if _, err := exec.LookPath("wp"); err != nil {
			return stepOutcome{Err: errors.New("wp-cli is missing; rerun the Cloudrifts installer so WordPress tooling is installed")}
		}
	}
	if err := a.validateActiveWebserverInstalled(); err != nil {
		return stepOutcome{Err: err}
	}
	php, err := detectPHP(normalizePHPVersion(a.spec.PHPVersion))
	if err != nil {
		return stepOutcome{Err: err}
	}
	a.php = php
	return stepOutcome{Detail: fmt.Sprintf("Ubuntu 24.04 ready · %s available · %s available", a.activeWebserver().Name, php.Service)}
}

func (a *applier) ensureAccount() stepOutcome {
	if a.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Would ensure group %s, user %s, %s, %s, tmp, logs, and starter index.php", a.spec.SystemGroup, a.spec.SystemUser, a.spec.HomeDir, a.spec.DocumentRoot)}
	}
	if _, err := user.LookupGroup(a.spec.SystemGroup); err != nil {
		if err := a.run("groupadd", "--system", a.spec.SystemGroup); err != nil {
			return stepOutcome{Err: err}
		}
		a.rollback = append(a.rollback, "created group "+a.spec.SystemGroup)
	}
	createdUser := false
	if _, err := user.Lookup(a.spec.SystemUser); err != nil {
		args := []string{"--create-home", "--home-dir", a.spec.HomeDir, "--shell", "/usr/sbin/nologin", "--gid", a.spec.SystemGroup, a.spec.SystemUser}
		if err := a.run("useradd", args...); err != nil {
			return stepOutcome{Err: err}
		}
		createdUser = true
		a.created = append(a.created, func() { _ = exec.Command("userdel", "-r", a.spec.SystemUser).Run() })
		a.rollback = append(a.rollback, "created user "+a.spec.SystemUser)
	}
	for _, dir := range []string{a.spec.HomeDir, a.spec.DocumentRoot, filepath.Join(a.spec.HomeDir, "tmp"), a.spec.LogDir, filepath.Join(a.spec.DocumentRoot, ".well-known", "acme-challenge")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return stepOutcome{Err: err}
		}
	}
	if err := os.Chmod(filepath.Join(a.spec.HomeDir, "tmp"), 0o700); err != nil {
		return stepOutcome{Err: err}
	}
	index := filepath.Join(a.spec.DocumentRoot, "index.php")
	if _, err := os.Stat(index); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(index, []byte(defaultIndexPHP(a.spec)), 0o644); err != nil {
			return stepOutcome{Err: err}
		}
	}
	fileGroup := "cloudrifts"
	if _, err := user.LookupGroup(fileGroup); err != nil {
		fileGroup = a.spec.SystemGroup
	}
	if err := a.run("chown", "-R", a.spec.SystemUser+":"+fileGroup, a.spec.HomeDir); err != nil {
		return stepOutcome{Err: err}
	}
	if err := os.Chmod(a.spec.HomeDir, 0o755); err != nil {
		return stepOutcome{Err: err}
	}
	for _, dir := range []string{a.spec.DocumentRoot, a.spec.LogDir} {
		if err := os.Chmod(dir, 0o2775); err != nil {
			return stepOutcome{Err: err}
		}
	}
	if err := os.Chmod(filepath.Join(a.spec.HomeDir, "tmp"), 0o700); err != nil {
		return stepOutcome{Err: err}
	}
	_ = filepath.WalkDir(a.spec.DocumentRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(path, 0o2775)
			return nil
		}
		_ = os.Chmod(path, 0o664)
		return nil
	})
	detail := fmt.Sprintf("User %s, document root %s, temp directory, logs, and starter index.php are ready", a.spec.SystemUser, a.spec.DocumentRoot)
	if !createdUser {
		detail = fmt.Sprintf("Existing user %s reused safely; document root %s is ready", a.spec.SystemUser, a.spec.DocumentRoot)
	}
	return stepOutcome{Detail: detail}
}

func (a *applier) ensureDatabase() stepOutcome {
	if !needsDatabase(a.spec.App) {
		return stepOutcome{Detail: "Plain PHP site selected; database is skipped until the user creates one from website settings."}
	}
	if a.spec.DatabaseName == "" || a.spec.DatabaseUser == "" || a.spec.DatabasePassword == "" {
		return stepOutcome{Err: errors.New("database credentials are missing from the job spec")}
	}
	if !safeSQLIdentifier(a.spec.DatabaseName) || !safeSQLIdentifier(a.spec.DatabaseUser) {
		return stepOutcome{Err: fmt.Errorf("unsafe database identifier generated for %s", a.spec.Domain)}
	}
	if a.dryRun {
		a.databaseCreated = true
		return stepOutcome{Detail: fmt.Sprintf("Would create MariaDB database %s and user %s with scoped grants", a.spec.DatabaseName, a.spec.DatabaseUser)}
	}
	sql := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;\n"+
		"CREATE USER IF NOT EXISTS '%s'@'localhost' IDENTIFIED BY '%s';\n"+
		"ALTER USER '%s'@'localhost' IDENTIFIED BY '%s';\n"+
		"GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'localhost';\n"+
		"FLUSH PRIVILEGES;\n",
		a.spec.DatabaseName, a.spec.DatabaseUser, escapeSQLString(a.spec.DatabasePassword), a.spec.DatabaseUser, escapeSQLString(a.spec.DatabasePassword), a.spec.DatabaseName, a.spec.DatabaseUser)
	if err := a.runInput(sql, "mysql", "--protocol=socket", "-uroot"); err != nil {
		return stepOutcome{Err: err}
	}
	a.databaseCreated = true
	a.created = append(a.created, func() {
		cleanup := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`; DROP USER IF EXISTS '%s'@'localhost'; FLUSH PRIVILEGES;", a.spec.DatabaseName, a.spec.DatabaseUser)
		_ = exec.Command("mysql", "--protocol=socket", "-uroot", "-e", cleanup).Run()
	})
	a.rollback = append(a.rollback, "created MariaDB database "+a.spec.DatabaseName+" and user "+a.spec.DatabaseUser)
	return stepOutcome{Detail: fmt.Sprintf("Database %s and user %s are ready", a.spec.DatabaseName, a.spec.DatabaseUser)}
}

func (a *applier) ensureApplication() stepOutcome {
	if !needsWordPress(a.spec.App) {
		return stepOutcome{Detail: "No application installer requested; starter PHP site remains in place."}
	}
	if a.spec.AppAdminUser == "" || a.spec.AppAdminPassword == "" || a.spec.AppAdminEmail == "" {
		return stepOutcome{Err: errors.New("WordPress admin credentials are missing from the job spec")}
	}
	if a.dryRun {
		detail := fmt.Sprintf("Would install WordPress for %s using database %s", a.spec.Domain, a.spec.DatabaseName)
		if strings.EqualFold(a.spec.App, "woocommerce") {
			detail += " and activate WooCommerce"
		}
		a.appInstalled = true
		return stepOutcome{Detail: detail}
	}
	wpPath := filepath.Join(a.spec.DocumentRoot, "wp-load.php")
	if _, err := os.Stat(wpPath); errors.Is(err, os.ErrNotExist) {
		if err := a.runAsSiteUser("wp", "core", "download", "--path="+a.spec.DocumentRoot, "--force"); err != nil {
			return stepOutcome{Err: err}
		}
	}
	configPath := filepath.Join(a.spec.DocumentRoot, "wp-config.php")
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		if err := a.runAsSiteUser("wp", "config", "create", "--path="+a.spec.DocumentRoot, "--dbname="+a.spec.DatabaseName, "--dbuser="+a.spec.DatabaseUser, "--dbpass="+a.spec.DatabasePassword, "--dbhost=localhost", "--skip-check"); err != nil {
			return stepOutcome{Err: err}
		}
	}
	if err := a.runAsSiteUser("wp", "core", "is-installed", "--path="+a.spec.DocumentRoot); err != nil {
		url := "http://" + a.spec.Domain
		if a.spec.DNSOK {
			url = "https://" + a.spec.Domain
		}
		title := strings.TrimSpace(a.spec.AppTitle)
		if title == "" {
			title = a.spec.Domain
		}
		if err := a.runAsSiteUser("wp", "core", "install", "--path="+a.spec.DocumentRoot, "--url="+url, "--title="+title, "--admin_user="+a.spec.AppAdminUser, "--admin_password="+a.spec.AppAdminPassword, "--admin_email="+a.spec.AppAdminEmail, "--skip-email"); err != nil {
			return stepOutcome{Err: err}
		}
	}
	if strings.EqualFold(a.spec.App, "woocommerce") {
		if err := a.runAsSiteUser("wp", "plugin", "install", "woocommerce", "--activate", "--path="+a.spec.DocumentRoot); err != nil {
			return stepOutcome{Err: err}
		}
	}
	if err := a.run("chown", "-R", a.spec.SystemUser+":"+a.spec.SystemGroup, a.spec.DocumentRoot); err != nil {
		return stepOutcome{Err: err}
	}
	a.appInstalled = true
	return stepOutcome{Detail: appInstallDetail(a.spec)}
}

func (a *applier) ensurePHPPool() stepOutcome {
	poolName := safePoolName(a.spec.SiteID)
	poolPath := filepath.Join(a.php.PoolDir, poolName+".conf")
	socket := phpSocketPath(poolName)
	content := phpPoolConfig(poolName, socket, a.spec)
	if a.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Would write %s and validate %s -t", poolPath, a.php.FPMBin)}
	}
	if err := os.MkdirAll(a.php.PoolDir, 0o755); err != nil {
		return stepOutcome{Err: err}
	}
	old, hadOld := readIfExists(poolPath)
	if err := os.WriteFile(poolPath, []byte(content), 0o644); err != nil {
		return stepOutcome{Err: err}
	}
	a.created = append(a.created, func() {
		if hadOld {
			_ = os.WriteFile(poolPath, old, 0o644)
		} else {
			_ = os.Remove(poolPath)
		}
	})
	a.rollback = append(a.rollback, "wrote PHP-FPM pool "+poolPath)
	if err := a.run(a.php.FPMBin, "-t"); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: fmt.Sprintf("Pool %s listens on %s and passed %s -t", poolName, socket, a.php.FPMBin)}
}

func (a *applier) ensureNginxHTTP() stepOutcome {
	available := nginxAvailablePath(a.spec.SiteID)
	enabled := nginxEnabledPath(a.spec.SiteID)
	content := nginxHTTPConfig(a.spec, phpSocketPath(safePoolName(a.spec.SiteID)))
	if a.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Would write %s, enable symlink, and run nginx -t", available)}
	}
	old, hadOld := readIfExists(available)
	if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
		return stepOutcome{Err: err}
	}
	a.created = append(a.created, func() {
		_ = os.Remove(enabled)
		if hadOld {
			_ = os.WriteFile(available, old, 0o644)
		} else {
			_ = os.Remove(available)
		}
	})
	a.rollback = append(a.rollback, "wrote Nginx vhost "+available)
	_ = os.Remove(enabled)
	if err := os.Symlink(available, enabled); err != nil {
		return stepOutcome{Err: err}
	}
	if err := a.run("nginx", "-t"); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: fmt.Sprintf("HTTP vhost enabled for %s and nginx -t passed", a.spec.Domain)}
}

func (a *applier) reloadServices() stepOutcome {
	if a.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Would reload %s and %s after successful config validation", a.php.Service, a.activeWebserver().Name)}
	}
	if err := a.run("systemctl", "reload-or-restart", a.php.Service); err != nil {
		return stepOutcome{Err: err}
	}
	if err := a.reloadActiveWebserver(); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: fmt.Sprintf("Reloaded %s and %s", a.php.Service, a.activeWebserver().Name)}
}

func (a *applier) ensureSSL() stepOutcome {
	if !a.spec.DNSOK {
		issue := Issue{Code: "ssl_blocked_by_dns", Severity: "warning", Title: "SSL waits for DNS", Detail: fmt.Sprintf("%s is not pointing to %s yet, so Let's Encrypt cannot validate the site.", a.spec.Domain, a.spec.RequiredAValue), Fix: fmt.Sprintf("Create A record %s → %s, wait for propagation, then click Retry SSL.", a.spec.RequiredAName, a.spec.RequiredAValue), Retryable: true}
		return stepOutcome{Detail: issue.Detail, Status: StepWarning, Issues: []Issue{issue}}
	}
	if a.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Would request Let's Encrypt certificate for %s with webroot %s, then rewrite %s SSL vhost", a.spec.Domain, a.spec.DocumentRoot, a.activeWebserver().Name)}
	}
	if _, err := exec.LookPath("certbot"); err != nil {
		issue := Issue{Code: "certbot_missing", Severity: "warning", Title: "Certificate worker is not installed", Detail: "The website is online over HTTP, but certbot is missing so SSL was not issued.", Fix: "Run the Cloudrifts installer again so certbot is installed, then click Retry SSL.", Retryable: true}
		return stepOutcome{Detail: issue.Detail, Status: StepWarning, Issues: []Issue{issue}}
	}
	certArgs := []string{"certonly", "--webroot", "--webroot-path", a.spec.DocumentRoot, "--domain", a.spec.Domain, "--non-interactive", "--agree-tos", "--register-unsafely-without-email", "--keep-until-expiring"}
	if err := a.run("certbot", certArgs...); err != nil {
		issue := Issue{Code: "certificate_issue_failed", Severity: "warning", Title: "Let's Encrypt could not issue SSL", Detail: err.Error(), Fix: "Open website diagnostics to see the exact DNS/HTTP challenge reason, fix it, then click Retry SSL.", Retryable: true}
		return stepOutcome{Detail: issue.Detail, Status: StepWarning, Issues: []Issue{issue}}
	}
	certPath := "/etc/letsencrypt/live/" + a.spec.Domain + "/fullchain.pem"
	keyPath := "/etc/letsencrypt/live/" + a.spec.Domain + "/privkey.pem"
	if err := a.activateSSLVHost(certPath, keyPath); err != nil {
		return stepOutcome{Err: err}
	}
	a.sslIssued = true
	a.certPath = certPath
	return stepOutcome{Detail: "Let's Encrypt certificate issued and HTTPS vhost activated for " + a.spec.Domain}
}

func (a *applier) finish(defaultStatus ResultStatus) Result {
	status := defaultStatus
	for _, step := range a.steps {
		if step.Status == StepFailed {
			status = ResultFailed
			break
		}
		if step.Status == StepWarning && status != ResultFailed {
			status = ResultWarning
		}
	}
	if a.dryRun && status == ResultDone && !a.spec.DNSOK {
		status = ResultWarning
	}
	a.log("website provision finished with %s", status)
	return Result{Status: status, Steps: a.steps, Issues: a.issues, Logs: a.logs, Rollback: a.rollback, SSLIssued: a.sslIssued, Certificate: a.certPath, DatabaseCreated: a.databaseCreated, AppInstalled: a.appInstalled, DryRun: a.dryRun}
}

func (a *applier) runRollback() {
	if a.dryRun || len(a.created) == 0 {
		return
	}
	a.log("rollback started")
	for i := len(a.created) - 1; i >= 0; i-- {
		a.created[i]()
	}
	_ = a.validateActiveWebserverConfig()
	_ = a.reloadActiveWebserver()
	if a.php.Service != "" {
		_ = exec.Command("systemctl", "reload-or-restart", a.php.Service).Run()
	}
	a.log("rollback finished")
}

func (a *applier) run(name string, args ...string) error {
	cmd := exec.CommandContext(a.ctx, name, args...)
	out, err := cmd.CombinedOutput()
	line := strings.TrimSpace(string(out))
	if line != "" {
		a.log("%s %s → %s", name, strings.Join(args, " "), line)
	} else {
		a.log("%s %s", name, strings.Join(args, " "))
	}
	if err != nil {
		if line == "" {
			line = err.Error()
		}
		return fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), line)
	}
	return nil
}

func (a *applier) runInput(input string, name string, args ...string) error {
	cmd := exec.CommandContext(a.ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	line := strings.TrimSpace(string(out))
	if line != "" {
		a.log("%s %s → %s", name, strings.Join(args, " "), line)
	} else {
		a.log("%s %s", name, strings.Join(args, " "))
	}
	if err != nil {
		if line == "" {
			line = err.Error()
		}
		return fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), line)
	}
	return nil
}

func (a *applier) runAsSiteUser(name string, args ...string) error {
	full := append([]string{"-u", a.spec.SystemUser, name}, args...)
	return a.run("sudo", full...)
}

func (a *applier) log(format string, args ...any) {
	a.logs = append(a.logs, time.Now().UTC().Format(time.RFC3339)+" "+fmt.Sprintf(format, args...))
}

func recoveryForStep(id string, spec WebsiteSpec) string {
	switch id {
	case "validate":
		return "Install on Ubuntu 24.04 with the Cloudrifts installer so Nginx, PHP-FPM, certbot, and the root agent are available."
	case "account":
		return "Check Linux user/group limits and filesystem permissions, then retry website creation."
	case "runtime":
		return "Fix the PHP-FPM pool error shown in the job log, then retry. Cloudrifts rolls back the broken pool automatically."
	case "vhost":
		return "Fix the webserver validation error shown in the job log, then retry. Cloudrifts rolls back the broken vhost automatically."
	case "reload":
		return "Check service status for Nginx/PHP-FPM, then retry after the service is healthy."
	case "ssl":
		return "Check DNS and HTTP challenge reachability for " + spec.Domain + ", then retry SSL."
	default:
		return "Fix the reported issue and retry the job."
	}
}

func firstIssueFix(issues []Issue) string {
	if len(issues) == 0 {
		return "needs attention"
	}
	return issues[0].Fix
}

func needsDatabase(app string) bool {
	return strings.EqualFold(app, "wordpress") || strings.EqualFold(app, "woocommerce")
}

func needsWordPress(app string) bool {
	return needsDatabase(app)
}

func safeSQLIdentifier(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func escapeSQLString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func appInstallDetail(spec WebsiteSpec) string {
	if strings.EqualFold(spec.App, "woocommerce") {
		return "WordPress installed and WooCommerce activated for " + spec.Domain
	}
	return "WordPress installed for " + spec.Domain
}

func normalizePHPVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return "8.3"
	}
	return version
}

func detectPHP(version string) (phpRuntime, error) {
	php := phpRuntime{Version: version, Service: "php" + version + "-fpm", PoolDir: "/etc/php/" + version + "/fpm/pool.d", FPMBin: "php-fpm" + version}
	if _, err := os.Stat(php.PoolDir); err != nil {
		return phpRuntime{}, fmt.Errorf("PHP-FPM pool directory %s is missing; install php%s-fpm or rerun installer", php.PoolDir, version)
	}
	if _, err := exec.LookPath(php.FPMBin); err != nil {
		return phpRuntime{}, fmt.Errorf("%s is missing; install php%s-fpm or rerun installer", php.FPMBin, version)
	}
	return php, nil
}

func isUbuntu2404() bool {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return false
	}
	text := string(b)
	return strings.Contains(text, "ID=ubuntu") && strings.Contains(text, "VERSION_ID=\"24.04\"")
}

func readIfExists(path string) ([]byte, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return b, true
}

func nginxAvailablePath(siteID string) string {
	return "/etc/nginx/sites-available/cloudrifts-" + safePoolName(siteID) + ".conf"
}

func nginxEnabledPath(siteID string) string {
	return "/etc/nginx/sites-enabled/cloudrifts-" + safePoolName(siteID) + ".conf"
}

func phpSocketPath(poolName string) string {
	return "/run/php/cloudrifts-" + poolName + ".sock"
}

func safePoolName(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "site"
	}
	if len(out) > 44 {
		out = out[:44]
	}
	return out
}

func defaultIndexPHP(spec WebsiteSpec) string {
	return fmt.Sprintf(`<?php
http_response_code(200);
?><!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s · Cloudrifts</title>
  <style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#07111f;color:#f8fbff;font-family:Inter,system-ui,sans-serif}.card{max-width:720px;padding:48px;border:1px solid rgba(255,255,255,.14);border-radius:32px;background:linear-gradient(135deg,rgba(255,255,255,.12),rgba(255,255,255,.04));box-shadow:0 30px 90px rgba(0,0,0,.36)}span{color:#8ef0ff}code{color:#b7f5c9}</style>
</head>
<body><main class="card"><p><span>Cloudrifts site is live</span></p><h1>%s</h1><p>PHP is working from <code>%s</code>.</p></main></body>
</html>
`, spec.Domain, spec.Domain, spec.DocumentRoot)
}

func phpPoolConfig(poolName string, socket string, spec WebsiteSpec) string {
	return fmt.Sprintf(`[%s]
user = %s
group = %s
listen = %s
listen.owner = www-data
listen.group = www-data
listen.mode = 0660
pm = ondemand
pm.max_children = 10
pm.process_idle_timeout = 20s
pm.max_requests = 500
chdir = /
catch_workers_output = yes
php_admin_value[open_basedir] = %s:/tmp
php_admin_value[upload_tmp_dir] = %s
php_admin_value[session.save_path] = %s
php_admin_value[error_log] = %s
php_admin_flag[log_errors] = on
`, poolName, spec.SystemUser, spec.SystemGroup, socket, spec.HomeDir, filepath.Join(spec.HomeDir, "tmp"), filepath.Join(spec.HomeDir, "tmp"), filepath.Join(spec.LogDir, "php-error.log"))
}

func nginxHTTPConfig(spec WebsiteSpec, socket string) string {
	return fmt.Sprintf(`server {
    listen 80;
    listen [::]:80;
    server_name %s;

    root %s;
    index index.php index.html index.htm;

    access_log %s;
    error_log %s warn;

    client_max_body_size 256m;

    location ^~ /.well-known/acme-challenge/ {
        root %s;
        default_type "text/plain";
        try_files $uri =404;
        allow all;
    }

    location / {
        try_files $uri $uri/ /index.php?$args;
    }

    location ~ \.php$ {
        include snippets/fastcgi-php.conf;
        fastcgi_pass unix:%s;
        fastcgi_param CLOUDRIFTS_SITE_ID %s;
    }

    location ~ /\. {
        deny all;
    }
}
`, spec.Domain, spec.DocumentRoot, filepath.Join(spec.LogDir, "access.log"), filepath.Join(spec.LogDir, "error.log"), spec.DocumentRoot, socket, spec.SiteID)
}

func nginxSSLConfig(spec WebsiteSpec, socket string, certPath string, keyPath string) string {
	return fmt.Sprintf(`server {
    listen 80;
    listen [::]:80;
    server_name %s;

    root %s;

    location ^~ /.well-known/acme-challenge/ {
        root %s;
        default_type "text/plain";
        try_files $uri =404;
        allow all;
    }

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name %s;

    root %s;
    index index.php index.html index.htm;

    ssl_certificate %s;
    ssl_certificate_key %s;
    ssl_session_timeout 1d;
    ssl_session_cache shared:CloudriftsSSL:10m;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;

    access_log %s;
    error_log %s warn;

    client_max_body_size 256m;

    location / {
        try_files $uri $uri/ /index.php?$args;
    }

    location ~ \.php$ {
        include snippets/fastcgi-php.conf;
        fastcgi_pass unix:%s;
        fastcgi_param HTTPS on;
        fastcgi_param CLOUDRIFTS_SITE_ID %s;
    }

    location ~ /\. {
        deny all;
    }
}
`, spec.Domain, spec.DocumentRoot, spec.DocumentRoot, spec.Domain, spec.DocumentRoot, certPath, keyPath, filepath.Join(spec.LogDir, "access.log"), filepath.Join(spec.LogDir, "error.log"), socket, spec.SiteID)
}
