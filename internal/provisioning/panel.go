package provisioning

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type PanelSpec struct {
	JobID       string `json:"job_id"`
	PanelURL    string `json:"panel_url"`
	ServerIP    string `json:"server_ip"`
	BackendURL  string `json:"backend_url"`
	ACMEWebroot string `json:"acme_webroot"`
}

type panelApplier struct {
	ctx        context.Context
	spec       PanelSpec
	dryRun     bool
	steps      []StepResult
	issues     []Issue
	logs       []string
	rollback   []string
	dnsChecked bool
	dnsReady   bool
	sslIssued  bool
	certPath   string
}

func ApplySetupPanel(ctx context.Context, spec PanelSpec, dryRun bool) Result {
	spec.PanelURL = strings.Trim(strings.ToLower(strings.TrimSpace(spec.PanelURL)), ".")
	if spec.BackendURL == "" {
		spec.BackendURL = "http://127.0.0.1:8080"
	}
	if spec.ACMEWebroot == "" {
		spec.ACMEWebroot = "/var/lib/cloudrifts/acme"
	}
	p := &panelApplier{ctx: ctx, spec: spec, dryRun: dryRun}
	p.log("starting panel setup for %s", emptyAs(spec.PanelURL, "panel host"))
	if !p.runStep("validate", "Validate panel endpoint", "Check panel hostname, backend port, Nginx, certbot and server IP", p.validate) {
		return p.finish(ResultFailed)
	}
	p.runStep("dns_preflight", "Check panel DNS", "Verify panel hostname resolves to this server before trusted SSL is requested", p.checkDNS)
	if !p.runStep("http_vhost", "Install panel HTTP route", "Create Nginx reverse proxy and ACME challenge webroot for the panel URL", p.writeHTTPVhost) {
		return p.finish(ResultFailed)
	}
	if !p.runStep("http_reload", "Reload HTTP route", "Validate Nginx and reload the HTTP panel route", p.reloadNginx) {
		return p.finish(ResultFailed)
	}
	p.runStep("certificate", "Issue panel certificate", "Use Let's Encrypt when delegation is live; otherwise keep HTTPS online with a self-signed fallback", p.ensureCertificate)
	if !p.runStep("https_vhost", "Install panel HTTPS route", "Write HTTPS reverse proxy, HTTP redirect and ACME challenge route", p.writeHTTPSVhost) {
		return p.finish(ResultFailed)
	}
	if !p.runStep("https_reload", "Reload HTTPS route", "Validate Nginx and reload the final panel route", p.reloadNginx) {
		return p.finish(ResultFailed)
	}
	return p.finish(ResultDone)
}

func (p *panelApplier) runStep(id, title, detail string, fn func() stepOutcome) bool {
	started := time.Now().UTC()
	step := StepResult{ID: id, Title: title, Detail: detail, Status: StepRunning, StartedAt: &started}
	p.log("%s: %s", id, title)
	out := fn()
	ended := time.Now().UTC()
	step.EndedAt = &ended
	if out.Detail != "" {
		step.Detail = out.Detail
	}
	step.Issues = out.Issues
	if out.Err != nil {
		issue := Issue{Code: id + "_failed", Severity: "critical", Title: title + " failed", Detail: out.Err.Error(), Fix: panelRecovery(id), Retryable: true}
		step.Issues = append(step.Issues, issue)
		p.issues = append(p.issues, issue)
		step.Status = StepFailed
		p.log("%s failed: %s", id, out.Err.Error())
		p.steps = append(p.steps, step)
		return false
	}
	if out.Status == "" {
		out.Status = StepDone
	}
	step.Status = out.Status
	if len(out.Issues) > 0 {
		p.issues = append(p.issues, out.Issues...)
	}
	p.log("%s done", id)
	p.steps = append(p.steps, step)
	return true
}

func (p *panelApplier) validate() stepOutcome {
	if p.spec.PanelURL == "" || strings.ContainsAny(p.spec.PanelURL, " /\\") || !strings.Contains(p.spec.PanelURL, ".") {
		return stepOutcome{Err: errors.New("panel URL must be a hostname such as panel.example.com")}
	}
	if p.spec.ServerIP == "" {
		return stepOutcome{Err: errors.New("server IP is required")}
	}
	if p.dryRun {
		return stepOutcome{Detail: "Dry run: panel Nginx route and certificate flow would be configured"}
	}
	if runtime.GOOS != "linux" {
		return stepOutcome{Err: fmt.Errorf("real panel setup is supported on Ubuntu Linux only, current OS is %s", runtime.GOOS)}
	}
	if os.Geteuid() != 0 {
		return stepOutcome{Err: errors.New("cloudrifts-agent must run as root to configure the panel route")}
	}
	if !isUbuntu2404() {
		return stepOutcome{Err: errors.New("Cloudrifts Phase 1 supports Ubuntu 24.04 only")}
	}
	for _, cmd := range []string{"nginx", "systemctl"} {
		if _, err := lookupCommand(cmd); err != nil {
			return stepOutcome{Err: fmt.Errorf("%s is missing; rerun the Cloudrifts installer", cmd)}
		}
	}
	return stepOutcome{Detail: "Panel host, Nginx and backend route are valid"}
}

func (p *panelApplier) checkDNS() stepOutcome {
	ok, resolved := p.panelResolves()
	p.dnsChecked = true
	p.dnsReady = ok
	if ok {
		return stepOutcome{Detail: fmt.Sprintf("%s resolves to %s", p.spec.PanelURL, p.spec.ServerIP)}
	}
	issue := Issue{Code: "panel_dns_delegation_required", Severity: "warning", Title: "Panel DNS is not delegated yet", Detail: fmt.Sprintf("%s does not resolve to %s yet. Cloudrifts will prepare the DNS zone and HTTPS fallback, but Let's Encrypt waits until registrar glue and nameserver delegation are live.", p.spec.PanelURL, p.spec.ServerIP), Fix: fmt.Sprintf("At your registrar, create child nameserver/glue records ns1/%[1]s and ns2/%[1]s pointing to %[2]s, then set the domain nameservers to ns1.%[1]s and ns2.%[1]s. After propagation, retry panel SSL.", rootDomainForPanel(p.spec.PanelURL), p.spec.ServerIP), Retryable: true}
	if len(resolved) > 0 {
		issue.Detail = fmt.Sprintf("%s resolves to %s, but this server is %s.", p.spec.PanelURL, strings.Join(resolved, ", "), p.spec.ServerIP)
		issue.Fix = fmt.Sprintf("Update delegation/A records so %s resolves to %s, then retry panel SSL.", p.spec.PanelURL, p.spec.ServerIP)
	}
	return stepOutcome{Detail: issue.Detail, Status: StepWarning, Issues: []Issue{issue}}
}

func (p *panelApplier) writeHTTPVhost() stepOutcome {
	if p.dryRun {
		return stepOutcome{Detail: "Would write " + panelNginxPath() + " with HTTP proxy and ACME webroot"}
	}
	if err := os.MkdirAll(p.spec.ACMEWebroot, 0o755); err != nil {
		return stepOutcome{Err: err}
	}
	if err := os.MkdirAll(filepath.Dir(panelNginxPath()), 0o755); err != nil {
		return stepOutcome{Err: err}
	}
	content := panelHTTPConfig(p.spec)
	if err := os.WriteFile(panelNginxPath(), []byte(content), 0o644); err != nil {
		return stepOutcome{Err: err}
	}
	if err := ensurePanelSymlink(panelNginxPath(), panelNginxEnabledPath()); err != nil {
		return stepOutcome{Err: err}
	}
	p.rollback = append(p.rollback, "wrote "+panelNginxPath())
	return stepOutcome{Detail: "Panel HTTP route is ready for " + p.spec.PanelURL}
}

func (p *panelApplier) reloadNginx() stepOutcome {
	if p.dryRun {
		return stepOutcome{Detail: "Would run nginx -t and reload nginx"}
	}
	if err := p.run("nginx", "-t"); err != nil {
		return stepOutcome{Err: err}
	}
	if err := p.run("systemctl", "reload", "nginx"); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: "Nginx config validates and is reloaded"}
}

func (p *panelApplier) ensureCertificate() stepOutcome {
	certPath := "/etc/letsencrypt/live/" + p.spec.PanelURL + "/fullchain.pem"
	keyPath := "/etc/letsencrypt/live/" + p.spec.PanelURL + "/privkey.pem"
	if p.dryRun {
		if !p.dnsChecked {
			p.dnsReady, _ = p.panelResolves()
			p.dnsChecked = true
		}
		if p.dnsReady {
			p.certPath = certPath
			p.sslIssued = true
			return stepOutcome{Detail: "Would request Let's Encrypt certificate for " + p.spec.PanelURL}
		}
		issue := p.selfSignedIssue("panel_ssl_waiting_for_dns", "Trusted SSL waits for DNS", fmt.Sprintf("%s is not delegated to this server yet, so Let's Encrypt cannot validate it. A temporary HTTPS certificate is installed so the panel stays reachable.", p.spec.PanelURL), fmt.Sprintf("Create registrar glue for ns1/ns2 pointing to %s and delegate the domain to those nameservers, then retry panel SSL.", p.spec.ServerIP))
		p.certPath = snakeoilCertPath()
		return stepOutcome{Detail: issue.Detail, Status: StepWarning, Issues: []Issue{issue}}
	}
	ok, _ := p.panelResolves()
	if ok {
		if _, err := lookupCommand("certbot"); err != nil {
			issue := p.selfSignedIssue("certbot_missing", "Certbot is missing", "certbot is not installed, so Cloudrifts enabled HTTPS with the local fallback certificate.", "Rerun the installer so certbot is installed, then retry panel SSL.")
			p.certPath = snakeoilCertPath()
			return stepOutcome{Detail: issue.Detail, Status: StepWarning, Issues: []Issue{issue}}
		}
		args := []string{"certonly", "--webroot", "-w", p.spec.ACMEWebroot, "-d", p.spec.PanelURL, "--non-interactive", "--agree-tos", "--register-unsafely-without-email", "--keep-until-expiring"}
		if err := p.run("certbot", args...); err != nil {
			issue := p.selfSignedIssue("panel_certbot_failed", "Let's Encrypt failed", err.Error(), "Fix the certbot error shown in the job log, then retry panel SSL.")
			p.certPath = snakeoilCertPath()
			return stepOutcome{Detail: issue.Detail, Status: StepWarning, Issues: []Issue{issue}}
		}
		if _, err := os.Stat(certPath); err == nil {
			p.sslIssued = true
			p.certPath = certPath
			_ = keyPath
			return stepOutcome{Detail: "Let's Encrypt certificate is installed at " + certPath}
		}
	}
	issue := p.selfSignedIssue("panel_ssl_waiting_for_dns", "Trusted SSL waits for DNS", fmt.Sprintf("%s is not delegated to this server yet, so Let's Encrypt cannot validate it. A temporary HTTPS certificate is installed so the panel stays reachable.", p.spec.PanelURL), fmt.Sprintf("Create registrar glue for ns1/ns2 pointing to %s and delegate the domain to those nameservers, then retry panel SSL.", p.spec.ServerIP))
	p.certPath = snakeoilCertPath()
	return stepOutcome{Detail: issue.Detail, Status: StepWarning, Issues: []Issue{issue}}
}

func (p *panelApplier) writeHTTPSVhost() stepOutcome {
	cert, key := p.certPair()
	if p.dryRun {
		return stepOutcome{Detail: "Would write HTTPS panel route using " + cert}
	}
	if cert == "" || key == "" {
		return stepOutcome{Err: errors.New("no certificate path is available for HTTPS panel route")}
	}
	content := panelHTTPSConfig(p.spec, cert, key)
	if err := os.WriteFile(panelNginxPath(), []byte(content), 0o644); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: "Panel HTTPS route is ready for " + p.spec.PanelURL}
}

func (p *panelApplier) panelResolves() (bool, []string) {
	ctx, cancel := context.WithTimeout(p.ctx, 2500*time.Millisecond)
	defer cancel()
	resolved, err := net.DefaultResolver.LookupHost(ctx, p.spec.PanelURL)
	if err != nil || len(resolved) == 0 {
		return false, resolved
	}
	for _, value := range resolved {
		if value == p.spec.ServerIP {
			return true, resolved
		}
	}
	return false, resolved
}

func (p *panelApplier) selfSignedIssue(code, title, detail, fix string) Issue {
	return Issue{Code: code, Severity: "warning", Title: title, Detail: detail, Fix: fix, Retryable: true}
}

func (p *panelApplier) certPair() (string, string) {
	if p.sslIssued {
		return "/etc/letsencrypt/live/" + p.spec.PanelURL + "/fullchain.pem", "/etc/letsencrypt/live/" + p.spec.PanelURL + "/privkey.pem"
	}
	return snakeoilCertPath(), snakeoilKeyPath()
}

func (p *panelApplier) run(name string, args ...string) error {
	return runLoggedCommand(p.ctx, p.log, name, args...)
}

func (p *panelApplier) log(format string, args ...any) {
	p.logs = append(p.logs, time.Now().UTC().Format(time.RFC3339)+" "+fmt.Sprintf(format, args...))
}

func (p *panelApplier) finish(defaultStatus ResultStatus) Result {
	status := defaultStatus
	for _, step := range p.steps {
		if step.Status == StepFailed {
			status = ResultFailed
			break
		}
		if step.Status == StepWarning && status != ResultFailed {
			status = ResultWarning
		}
	}
	p.log("panel setup finished with %s", status)
	return Result{Status: status, Steps: p.steps, Issues: p.issues, Logs: p.logs, Rollback: p.rollback, SSLIssued: p.sslIssued, Certificate: p.certPath, DryRun: p.dryRun}
}

func panelRecovery(id string) string {
	switch id {
	case "validate":
		return "Rerun the Cloudrifts installer on Ubuntu 24.04 so nginx, certbot and the root agent are available."
	case "dns_preflight", "certificate":
		return "Fix registrar glue/delegation or the certbot issue shown, then retry panel SSL from setup."
	case "http_vhost", "https_vhost":
		return "Check /etc/nginx/sites-available permissions and retry setup."
	case "http_reload", "https_reload":
		return "Run nginx -t for the exact config error, fix it, then retry setup."
	default:
		return "Fix the reported setup issue and retry."
	}
}

func panelNginxPath() string        { return "/etc/nginx/sites-available/cloudrifts-panel.conf" }
func panelNginxEnabledPath() string { return "/etc/nginx/sites-enabled/cloudrifts-panel.conf" }
func snakeoilCertPath() string      { return "/etc/ssl/certs/ssl-cert-snakeoil.pem" }
func snakeoilKeyPath() string       { return "/etc/ssl/private/ssl-cert-snakeoil.key" }

func panelHTTPConfig(spec PanelSpec) string {
	return fmt.Sprintf(`server {
    listen 80;
    listen [::]:80;
    server_name %s;

    location ^~ /.well-known/acme-challenge/ {
        root %s;
        default_type text/plain;
        try_files $uri =404;
    }

    location / {
        proxy_pass %s;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
`, spec.PanelURL, spec.ACMEWebroot, spec.BackendURL)
}

func panelHTTPSConfig(spec PanelSpec, certPath, keyPath string) string {
	return fmt.Sprintf(`server {
    listen 80;
    listen [::]:80;
    server_name %s;

    location ^~ /.well-known/acme-challenge/ {
        root %s;
        default_type text/plain;
        try_files $uri =404;
    }

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name %s;

    ssl_certificate %s;
    ssl_certificate_key %s;
    ssl_session_timeout 1d;
    ssl_session_cache shared:CloudriftsPanelSSL:10m;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;

    location / {
        proxy_pass %s;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
`, spec.PanelURL, spec.ACMEWebroot, spec.PanelURL, certPath, keyPath, spec.BackendURL)
}

func rootDomainForPanel(host string) string {
	parts := strings.Split(strings.Trim(host, "."), ".")
	if len(parts) <= 2 {
		return strings.Trim(host, ".")
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func ensurePanelSymlink(target, link string) error {
	if existing, err := os.Readlink(link); err == nil {
		if existing == target {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		if err := os.Remove(link); err != nil {
			return err
		}
	}
	return os.Symlink(target, link)
}
