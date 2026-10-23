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

type MailRoleSpec struct {
	JobID      string `json:"job_id"`
	Hostname   string `json:"hostname"`
	WebmailURL string `json:"webmail_url"`
	ServerIP   string `json:"server_ip"`
}

type mailRoleApplier struct {
	ctx      context.Context
	spec     MailRoleSpec
	dryRun   bool
	steps    []StepResult
	issues   []Issue
	logs     []string
	rollback []string
	undo     []func()
}

func ApplySetupMailRole(ctx context.Context, spec MailRoleSpec, dryRun bool) Result {
	m := &mailRoleApplier{ctx: ctx, spec: normalizeMailRoleSpec(spec), dryRun: dryRun}
	m.log("starting mail role setup for %s", emptyAs(m.spec.Hostname, "mail host"))
	if !m.runStep("validate", "Validate mail role request", "Check Ubuntu, root helper, Postfix, Dovecot, hostname, and service manager", m.validate) {
		return m.finish(ResultFailed)
	}
	if !m.runStep("postfix", "Configure Postfix", "Set hostname, Maildir delivery, TLS defaults, and SMTP auth handoff", m.configurePostfix) {
		m.runRollback()
		return m.finish(ResultFailed)
	}
	if !m.runStep("dovecot", "Configure Dovecot", "Set Maildir storage, IMAP service, and Postfix auth socket", m.configureDovecot) {
		m.runRollback()
		return m.finish(ResultFailed)
	}
	if !m.runStep("service", "Start mail services", "Enable and restart Postfix and Dovecot after config checks", m.startServices) {
		m.runRollback()
		return m.finish(ResultFailed)
	}
	m.runStep("firewall", "Open mail firewall", "Open SMTP, submission, SMTPS, IMAP, and IMAPS when UFW is active", m.openFirewall)
	return m.finish(ResultDone)
}

func (m *mailRoleApplier) runStep(id, title, detail string, fn func() stepOutcome) bool {
	started := time.Now().UTC()
	step := StepResult{ID: id, Title: title, Detail: detail, Status: StepRunning, StartedAt: &started}
	m.log("%s: %s", id, title)
	out := fn()
	ended := time.Now().UTC()
	step.EndedAt = &ended
	if out.Detail != "" {
		step.Detail = out.Detail
	}
	step.Issues = out.Issues
	if out.Err != nil {
		issue := Issue{Code: "mail_" + id + "_failed", Severity: "critical", Title: title + " failed", Detail: out.Err.Error(), Fix: mailRoleRecovery(id), Retryable: true}
		step.Issues = append(step.Issues, issue)
		m.issues = append(m.issues, issue)
		step.Status = StepFailed
		m.log("%s failed: %s", id, out.Err.Error())
		m.steps = append(m.steps, step)
		return false
	}
	if out.Status == "" {
		out.Status = StepDone
	}
	step.Status = out.Status
	if len(out.Issues) > 0 {
		m.issues = append(m.issues, out.Issues...)
	}
	m.log("%s done", id)
	m.steps = append(m.steps, step)
	return true
}

func (m *mailRoleApplier) validate() stepOutcome {
	if m.spec.Hostname == "" {
		return stepOutcome{Err: errors.New("mail hostname is required")}
	}
	if m.dryRun {
		return stepOutcome{Detail: "Dry run: Postfix, Dovecot, service and firewall changes would be prepared"}
	}
	if runtime.GOOS != "linux" {
		return stepOutcome{Err: fmt.Errorf("real mail setup is supported on Ubuntu Linux only, current OS is %s", runtime.GOOS)}
	}
	if os.Geteuid() != 0 {
		return stepOutcome{Err: errors.New("cloudrifts-agent must run as root to configure the mail role")}
	}
	if !isUbuntu2404() {
		return stepOutcome{Err: errors.New("Cloudrifts Phase 1 supports Ubuntu 24.04 only")}
	}
	for _, cmd := range []string{"postconf", "postfix", "doveconf", "systemctl"} {
		if _, err := lookupCommand(cmd); err != nil {
			return stepOutcome{Err: fmt.Errorf("%s is missing; rerun the Cloudrifts installer so mail packages are installed", cmd)}
		}
	}
	return stepOutcome{Detail: "Ubuntu 24.04 and mail tooling are available"}
}

func (m *mailRoleApplier) configurePostfix() stepOutcome {
	if m.dryRun {
		return stepOutcome{Detail: "Would configure Postfix hostname, Maildir delivery, TLS, SASL auth and relay safety"}
	}
	oldMailname, hadMailname := readIfExists("/etc/mailname")
	if err := os.WriteFile("/etc/mailname", []byte(m.spec.Hostname+"\n"), 0o644); err != nil {
		return stepOutcome{Err: err}
	}
	m.undo = append(m.undo, func() {
		if hadMailname {
			_ = os.WriteFile("/etc/mailname", oldMailname, 0o644)
		} else {
			_ = os.Remove("/etc/mailname")
		}
	})
	m.rollback = append(m.rollback, "updated /etc/mailname")

	settings := []string{
		"myhostname = " + m.spec.Hostname,
		"myorigin = /etc/mailname",
		"mydestination = $myhostname, localhost.$mydomain, localhost",
		"inet_interfaces = all",
		"inet_protocols = all",
		"home_mailbox = Maildir/",
		"smtpd_tls_cert_file = /etc/ssl/certs/ssl-cert-snakeoil.pem",
		"smtpd_tls_key_file = /etc/ssl/private/ssl-cert-snakeoil.key",
		"smtpd_use_tls = yes",
		"smtpd_tls_auth_only = yes",
		"smtpd_sasl_type = dovecot",
		"smtpd_sasl_path = private/auth",
		"smtpd_sasl_auth_enable = yes",
		"smtpd_recipient_restrictions = permit_sasl_authenticated, permit_mynetworks, reject_unauth_destination",
	}
	for _, setting := range settings {
		if err := m.run("postconf", "-e", setting); err != nil {
			return stepOutcome{Err: err}
		}
	}
	return stepOutcome{Detail: "Postfix configured for local Maildir delivery and authenticated submission"}
}

func (m *mailRoleApplier) configureDovecot() stepOutcome {
	path := "/etc/dovecot/conf.d/99-cloudrifts-mail.conf"
	content := `protocols = imap lmtp
mail_location = maildir:~/Maildir
ssl = yes

service auth {
  unix_listener /var/spool/postfix/private/auth {
    mode = 0660
    user = postfix
    group = postfix
  }
}
`
	if m.dryRun {
		return stepOutcome{Detail: "Would write " + path}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return stepOutcome{Err: err}
	}
	old, hadOld := readIfExists(path)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return stepOutcome{Err: err}
	}
	m.undo = append(m.undo, func() {
		if hadOld {
			_ = os.WriteFile(path, old, 0o644)
		} else {
			_ = os.Remove(path)
		}
	})
	m.rollback = append(m.rollback, "wrote "+path)
	if err := m.run("doveconf", "-n"); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: "Dovecot configured for IMAP, LMTP, Maildir and Postfix auth socket"}
}

func (m *mailRoleApplier) startServices() stepOutcome {
	if m.dryRun {
		return stepOutcome{Detail: "Would enable and restart postfix and dovecot"}
	}
	for _, unit := range []string{"postfix", "dovecot"} {
		if err := m.run("systemctl", "enable", unit); err != nil {
			return stepOutcome{Err: err}
		}
		if err := m.run("systemctl", "restart", unit); err != nil {
			return stepOutcome{Err: err}
		}
		if err := m.run("systemctl", "is-active", "--quiet", unit); err != nil {
			return stepOutcome{Err: err}
		}
	}
	return stepOutcome{Detail: "Postfix and Dovecot are active"}
}

func (m *mailRoleApplier) openFirewall() stepOutcome {
	if m.dryRun {
		return stepOutcome{Detail: "Would open 25/tcp, 465/tcp, 587/tcp, 143/tcp and 993/tcp if UFW is active"}
	}
	if _, err := lookupCommand("ufw"); err != nil {
		return stepOutcome{Detail: "UFW is not installed; firewall changes skipped"}
	}
	out, _ := exec.CommandContext(m.ctx, "ufw", "status").CombinedOutput()
	if !strings.Contains(strings.ToLower(string(out)), "status: active") {
		return stepOutcome{Detail: "UFW is inactive; firewall changes skipped"}
	}
	for _, rule := range []string{"25/tcp", "465/tcp", "587/tcp", "143/tcp", "993/tcp"} {
		_ = m.run("ufw", "allow", rule)
	}
	return stepOutcome{Detail: "Mail firewall ports are open"}
}

func (m *mailRoleApplier) runRollback() {
	if m.dryRun || len(m.undo) == 0 {
		return
	}
	m.log("mail role rollback started")
	for i := len(m.undo) - 1; i >= 0; i-- {
		m.undo[i]()
	}
	m.log("mail role rollback finished")
}

func (m *mailRoleApplier) finish(defaultStatus ResultStatus) Result {
	status := defaultStatus
	for _, step := range m.steps {
		if step.Status == StepFailed {
			status = ResultFailed
			break
		}
		if step.Status == StepWarning && status != ResultFailed {
			status = ResultWarning
		}
	}
	m.log("mail role setup finished with %s", status)
	return Result{Status: status, Steps: m.steps, Issues: m.issues, Logs: m.logs, Rollback: m.rollback, DryRun: m.dryRun}
}

func (m *mailRoleApplier) run(name string, args ...string) error {
	return runLoggedCommand(m.ctx, m.log, name, args...)
}

func (m *mailRoleApplier) log(format string, args ...any) {
	m.logs = append(m.logs, time.Now().UTC().Format(time.RFC3339)+" "+fmt.Sprintf(format, args...))
}

func normalizeMailRoleSpec(spec MailRoleSpec) MailRoleSpec {
	spec.Hostname = strings.Trim(strings.ToLower(strings.TrimSpace(spec.Hostname)), ".")
	spec.WebmailURL = strings.Trim(strings.ToLower(strings.TrimSpace(spec.WebmailURL)), ".")
	spec.ServerIP = strings.TrimSpace(spec.ServerIP)
	return spec
}

func mailRoleRecovery(id string) string {
	switch id {
	case "validate":
		return "Run the Cloudrifts installer again so postfix, dovecot-imapd, dovecot-lmtpd and systemd are available."
	case "postfix":
		return "Open the job log for the exact postconf error, fix the value shown, then retry mail setup."
	case "dovecot":
		return "Open the job log for the exact doveconf error, fix the generated Dovecot config, then retry mail setup."
	case "service":
		return "Check systemctl status postfix dovecot and journalctl output, then retry after the services are healthy."
	case "firewall":
		return "Open SMTP/IMAP ports manually if UFW blocked the automatic rule."
	default:
		return "Fix the reported mail setup issue and retry."
	}
}
