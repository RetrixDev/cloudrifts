package provisioning

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type DNSRoleSpec struct {
	JobID       string `json:"job_id"`
	ServerIP    string `json:"server_ip"`
	Nameserver1 string `json:"nameserver_1"`
	Nameserver2 string `json:"nameserver_2"`
}

type DNSZoneSpec struct {
	JobID       string          `json:"job_id"`
	ZoneID      string          `json:"zone_id"`
	Domain      string          `json:"domain"`
	ServerIP    string          `json:"server_ip"`
	Nameserver1 string          `json:"nameserver_1"`
	Nameserver2 string          `json:"nameserver_2"`
	Records     []DNSRecordSpec `json:"records"`
}

type DNSRecordSpec struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

type dnsRoleApplier struct {
	ctx      context.Context
	spec     DNSRoleSpec
	dryRun   bool
	steps    []StepResult
	issues   []Issue
	logs     []string
	rollback []string
	undo     []func()
}

type dnsZoneApplier struct {
	ctx      context.Context
	spec     DNSZoneSpec
	dryRun   bool
	steps    []StepResult
	issues   []Issue
	logs     []string
	rollback []string
}

const powerDNSDBPath = "/var/lib/powerdns/cloudrifts.sqlite3"
const powerDNSConfigPath = "/etc/powerdns/pdns.d/cloudrifts-sqlite3.conf"

func ApplySetupDNSRole(ctx context.Context, spec DNSRoleSpec, dryRun bool) Result {
	d := &dnsRoleApplier{ctx: ctx, spec: spec, dryRun: dryRun}
	d.log("starting PowerDNS role setup for %s", emptyAs(spec.ServerIP, "server"))
	if !d.runStep("validate", "Validate DNS role request", "Check Ubuntu, root helper, PowerDNS, sqlite3, nameservers, and server IP", d.validate) {
		return d.finish(ResultFailed)
	}
	if !d.runStep("database", "Prepare PowerDNS database", "Create the SQLite backend database and schema for authoritative zones", d.prepareDatabase) {
		d.runRollback()
		return d.finish(ResultFailed)
	}
	if !d.runStep("config", "Write PowerDNS config", "Point PowerDNS to the Cloudrifts SQLite backend", d.writeConfig) {
		d.runRollback()
		return d.finish(ResultFailed)
	}
	if !d.runStep("service", "Start DNS service", "Enable and restart PowerDNS after config validation", d.startService) {
		d.runRollback()
		return d.finish(ResultFailed)
	}
	d.runStep("firewall", "Open DNS firewall", "Open TCP/UDP 53 when UFW is active", d.openFirewall)
	return d.finish(ResultDone)
}

func ApplyDNSZone(ctx context.Context, spec DNSZoneSpec, dryRun bool) Result {
	z := &dnsZoneApplier{ctx: ctx, spec: normalizeDNSZoneSpec(spec), dryRun: dryRun}
	z.log("starting DNS zone apply for %s with %d records", z.spec.Domain, len(z.spec.Records))
	if !z.runStep("validate", "Validate zone", "Check domain, records, nameservers, PowerDNS tooling, and authoritative service", z.validate) {
		return z.finish(ResultFailed)
	}
	if !z.runStep("zone", "Create or update zone", "Ensure the authoritative DNS zone exists in PowerDNS", z.ensureZone) {
		return z.finish(ResultFailed)
	}
	if !z.runStep("records", "Apply records", "Replace managed NS, A, CNAME, MX, TXT, AAAA records with the panel state", z.applyRecords) {
		return z.finish(ResultFailed)
	}
	if !z.runStep("reload", "Reload DNS service", "Rectify the zone and reload PowerDNS", z.reload) {
		return z.finish(ResultFailed)
	}
	return z.finish(ResultDone)
}

func (d *dnsRoleApplier) runStep(id, title, detail string, fn func() stepOutcome) bool {
	started := time.Now().UTC()
	step := StepResult{ID: id, Title: title, Detail: detail, Status: StepRunning, StartedAt: &started}
	d.log("%s: %s", id, title)
	out := fn()
	ended := time.Now().UTC()
	step.EndedAt = &ended
	if out.Detail != "" {
		step.Detail = out.Detail
	}
	step.Issues = out.Issues
	if out.Err != nil {
		issue := Issue{Code: id + "_failed", Severity: "critical", Title: title + " failed", Detail: out.Err.Error(), Fix: dnsRoleRecovery(id), Retryable: true}
		step.Issues = append(step.Issues, issue)
		d.issues = append(d.issues, issue)
		step.Status = StepFailed
		d.log("%s failed: %s", id, out.Err.Error())
		d.steps = append(d.steps, step)
		return false
	}
	if out.Status == "" {
		out.Status = StepDone
	}
	step.Status = out.Status
	if len(out.Issues) > 0 {
		d.issues = append(d.issues, out.Issues...)
	}
	d.log("%s done", id)
	d.steps = append(d.steps, step)
	return true
}

func (z *dnsZoneApplier) runStep(id, title, detail string, fn func() stepOutcome) bool {
	started := time.Now().UTC()
	step := StepResult{ID: id, Title: title, Detail: detail, Status: StepRunning, StartedAt: &started}
	z.log("%s: %s", id, title)
	out := fn()
	ended := time.Now().UTC()
	step.EndedAt = &ended
	if out.Detail != "" {
		step.Detail = out.Detail
	}
	step.Issues = out.Issues
	if out.Err != nil {
		issue := Issue{Code: id + "_failed", Severity: "critical", Title: title + " failed", Detail: out.Err.Error(), Fix: dnsZoneRecovery(id), Retryable: true}
		step.Issues = append(step.Issues, issue)
		z.issues = append(z.issues, issue)
		step.Status = StepFailed
		z.log("%s failed: %s", id, out.Err.Error())
		z.steps = append(z.steps, step)
		return false
	}
	if out.Status == "" {
		out.Status = StepDone
	}
	step.Status = out.Status
	if len(out.Issues) > 0 {
		z.issues = append(z.issues, out.Issues...)
	}
	z.log("%s done", id)
	z.steps = append(z.steps, step)
	return true
}

func (d *dnsRoleApplier) validate() stepOutcome {
	if d.spec.ServerIP == "" {
		return stepOutcome{Err: errors.New("server IP is required for DNS role setup")}
	}
	if d.spec.Nameserver1 == "" || d.spec.Nameserver2 == "" {
		return stepOutcome{Err: errors.New("nameserver hostnames are required")}
	}
	if d.dryRun {
		return stepOutcome{Detail: "Dry run: PowerDNS, sqlite backend, service and firewall changes would be prepared"}
	}
	if runtime.GOOS != "linux" {
		return stepOutcome{Err: fmt.Errorf("real DNS setup is supported on Ubuntu Linux only, current OS is %s", runtime.GOOS)}
	}
	if os.Geteuid() != 0 {
		return stepOutcome{Err: errors.New("cloudrifts-agent must run as root to configure PowerDNS")}
	}
	if !isUbuntu2404() {
		return stepOutcome{Err: errors.New("Cloudrifts Phase 1 supports Ubuntu 24.04 only")}
	}
	for _, cmd := range []string{"pdns_server", "pdnsutil", "sqlite3", "systemctl"} {
		if _, err := lookupCommand(cmd); err != nil {
			return stepOutcome{Err: fmt.Errorf("%s is missing; rerun the Cloudrifts installer so PowerDNS packages are installed", cmd)}
		}
	}
	return stepOutcome{Detail: "Ubuntu 24.04 and PowerDNS tooling are available"}
}

func (d *dnsRoleApplier) prepareDatabase() stepOutcome {
	if d.dryRun {
		return stepOutcome{Detail: "Would create " + powerDNSDBPath + " and import PowerDNS SQLite schema"}
	}
	if err := os.MkdirAll(filepath.Dir(powerDNSDBPath), 0o750); err != nil {
		return stepOutcome{Err: err}
	}
	if _, err := os.Stat(powerDNSDBPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(powerDNSDBPath, []byte{}, 0o640); err != nil {
			return stepOutcome{Err: err}
		}
		d.undo = append(d.undo, func() { _ = os.Remove(powerDNSDBPath) })
		d.rollback = append(d.rollback, "created "+powerDNSDBPath)
	}
	if err := runSQLiteSchema(d.ctx, powerDNSDBPath); err != nil {
		return stepOutcome{Err: err}
	}
	_ = exec.Command("chown", "pdns:pdns", powerDNSDBPath).Run()
	return stepOutcome{Detail: "PowerDNS SQLite database is ready at " + powerDNSDBPath}
}

func (d *dnsRoleApplier) writeConfig() stepOutcome {
	if d.dryRun {
		return stepOutcome{Detail: "Would write " + powerDNSConfigPath}
	}
	if err := os.MkdirAll(filepath.Dir(powerDNSConfigPath), 0o755); err != nil {
		return stepOutcome{Err: err}
	}
	old, hadOld := readIfExists(powerDNSConfigPath)
	bindIP := dnsBindIPv4(d.spec.ServerIP)
	content := fmt.Sprintf(`launch=gsqlite3
gsqlite3-database=%s
local-address=%s
local-port=53
setgid=pdns
setuid=pdns
`, powerDNSDBPath, bindIP)
	if err := os.WriteFile(powerDNSConfigPath, []byte(content), 0o640); err != nil {
		return stepOutcome{Err: err}
	}
	d.undo = append(d.undo, func() {
		if hadOld {
			_ = os.WriteFile(powerDNSConfigPath, old, 0o640)
		} else {
			_ = os.Remove(powerDNSConfigPath)
		}
	})
	d.rollback = append(d.rollback, "wrote "+powerDNSConfigPath)
	return stepOutcome{Detail: "PowerDNS SQLite backend config written"}
}

func dnsBindIPv4(preferred string) string {
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		ifaces, err := net.Interfaces()
		if err == nil {
			for _, iface := range ifaces {
				if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
					continue
				}
				addrs, _ := iface.Addrs()
				for _, addr := range addrs {
					ip, _, err := net.ParseCIDR(addr.String())
					if err == nil && ip != nil && ip.To4() != nil && ip.String() == preferred {
						return preferred
					}
				}
			}
		}
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && ip != nil && ip.To4() != nil {
				return ip.String()
			}
		}
	}
	return "127.0.0.1"
}

func (d *dnsRoleApplier) startService() stepOutcome {
	if d.dryRun {
		return stepOutcome{Detail: "Would enable and restart pdns"}
	}
	if err := d.run("systemctl", "enable", "pdns"); err != nil {
		return stepOutcome{Err: err}
	}
	if err := d.run("systemctl", "restart", "pdns"); err != nil {
		return stepOutcome{Err: err}
	}
	if err := d.run("systemctl", "is-active", "--quiet", "pdns"); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: "PowerDNS is active"}
}

func (d *dnsRoleApplier) openFirewall() stepOutcome {
	if d.dryRun {
		return stepOutcome{Detail: "Would open 53/tcp and 53/udp if UFW is active"}
	}
	if _, err := lookupCommand("ufw"); err != nil {
		return stepOutcome{Detail: "UFW is not installed; firewall changes skipped"}
	}
	out, _ := exec.CommandContext(d.ctx, "ufw", "status").CombinedOutput()
	if !strings.Contains(strings.ToLower(string(out)), "status: active") {
		return stepOutcome{Detail: "UFW is inactive; firewall changes skipped"}
	}
	_ = d.run("ufw", "allow", "53/tcp")
	_ = d.run("ufw", "allow", "53/udp")
	return stepOutcome{Detail: "DNS firewall ports are open"}
}

func (z *dnsZoneApplier) validate() stepOutcome {
	if z.spec.Domain == "" {
		return stepOutcome{Err: errors.New("zone domain is required")}
	}
	if z.spec.Nameserver1 == "" || z.spec.Nameserver2 == "" {
		return stepOutcome{Err: errors.New("nameservers are required")}
	}
	for _, record := range z.spec.Records {
		if record.Name == "" || record.Type == "" || record.Content == "" {
			return stepOutcome{Err: errors.New("DNS record name, type, and content are required")}
		}
	}
	if z.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Dry run: would apply %d records for %s", len(z.spec.Records), z.spec.Domain)}
	}
	if runtime.GOOS != "linux" {
		return stepOutcome{Err: fmt.Errorf("real DNS zone apply is supported on Ubuntu Linux only, current OS is %s", runtime.GOOS)}
	}
	if os.Geteuid() != 0 {
		return stepOutcome{Err: errors.New("cloudrifts-agent must run as root to update PowerDNS zones")}
	}
	if !isUbuntu2404() {
		return stepOutcome{Err: errors.New("Cloudrifts Phase 1 supports Ubuntu 24.04 only")}
	}
	for _, cmd := range []string{"pdnsutil", "systemctl"} {
		if _, err := lookupCommand(cmd); err != nil {
			return stepOutcome{Err: fmt.Errorf("%s is missing; run DNS role setup first", cmd)}
		}
	}
	return stepOutcome{Detail: "Zone and record request is valid"}
}

func (z *dnsZoneApplier) ensureZone() stepOutcome {
	if z.dryRun {
		return stepOutcome{Detail: "Would create zone " + z.spec.Domain + " if missing"}
	}
	if err := z.run("pdnsutil", "show-zone", z.spec.Domain); err == nil {
		return stepOutcome{Detail: "Zone " + z.spec.Domain + " already exists"}
	}
	if err := z.run("pdnsutil", "create-zone", z.spec.Domain, z.spec.Nameserver1); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: "Zone " + z.spec.Domain + " created"}
}

func (z *dnsZoneApplier) applyRecords() stepOutcome {
	if z.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Would replace %d managed rrsets", len(z.spec.Records)+2)}
	}
	records := append([]DNSRecordSpec{
		{Name: z.spec.Domain, Type: "NS", Content: ensureTrailingDot(z.spec.Nameserver1), TTL: 3600},
		{Name: z.spec.Domain, Type: "NS", Content: ensureTrailingDot(z.spec.Nameserver2), TTL: 3600},
	}, z.spec.Records...)
	grouped := map[string][]DNSRecordSpec{}
	for _, record := range records {
		record = normalizeDNSRecord(z.spec.Domain, record)
		key := record.Name + "\x00" + record.Type
		grouped[key] = append(grouped[key], record)
	}
	for _, set := range grouped {
		if len(set) == 0 {
			continue
		}
		first := set[0]
		args := []string{"replace-rrset", z.spec.Domain, first.Name, first.Type, fmt.Sprintf("%d", first.TTL)}
		for _, record := range set {
			args = append(args, record.Content)
		}
		if err := z.run("pdnsutil", args...); err != nil {
			return stepOutcome{Err: err}
		}
	}
	return stepOutcome{Detail: fmt.Sprintf("Applied %d managed records", len(records))}
}

func (z *dnsZoneApplier) reload() stepOutcome {
	if z.dryRun {
		return stepOutcome{Detail: "Would rectify zone and reload pdns"}
	}
	if err := z.run("pdnsutil", "rectify-zone", z.spec.Domain); err != nil {
		return stepOutcome{Err: err}
	}
	if err := z.run("systemctl", "reload-or-restart", "pdns"); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: "PowerDNS reloaded for " + z.spec.Domain}
}

func (d *dnsRoleApplier) runRollback() {
	if d.dryRun || len(d.undo) == 0 {
		return
	}
	d.log("DNS role rollback started")
	for i := len(d.undo) - 1; i >= 0; i-- {
		d.undo[i]()
	}
	d.log("DNS role rollback finished")
}

func (d *dnsRoleApplier) finish(defaultStatus ResultStatus) Result {
	status := defaultStatus
	for _, step := range d.steps {
		if step.Status == StepFailed {
			status = ResultFailed
			break
		}
		if step.Status == StepWarning && status != ResultFailed {
			status = ResultWarning
		}
	}
	d.log("DNS role setup finished with %s", status)
	return Result{Status: status, Steps: d.steps, Issues: d.issues, Logs: d.logs, Rollback: d.rollback, DryRun: d.dryRun}
}

func (z *dnsZoneApplier) finish(defaultStatus ResultStatus) Result {
	status := defaultStatus
	for _, step := range z.steps {
		if step.Status == StepFailed {
			status = ResultFailed
			break
		}
		if step.Status == StepWarning && status != ResultFailed {
			status = ResultWarning
		}
	}
	z.log("DNS zone apply finished with %s", status)
	return Result{Status: status, Steps: z.steps, Issues: z.issues, Logs: z.logs, Rollback: z.rollback, DryRun: z.dryRun}
}

func (d *dnsRoleApplier) run(name string, args ...string) error {
	return runLoggedCommand(d.ctx, d.log, name, args...)
}

func (z *dnsZoneApplier) run(name string, args ...string) error {
	return runLoggedCommand(z.ctx, z.log, name, args...)
}

func (d *dnsRoleApplier) log(format string, args ...any) {
	d.logs = append(d.logs, time.Now().UTC().Format(time.RFC3339)+" "+fmt.Sprintf(format, args...))
}

func (z *dnsZoneApplier) log(format string, args ...any) {
	z.logs = append(z.logs, time.Now().UTC().Format(time.RFC3339)+" "+fmt.Sprintf(format, args...))
}

func runLoggedCommand(ctx context.Context, log func(string, ...any), name string, args ...string) error {
	cmdPath, err := lookupCommand(name)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, cmdPath, args...)
	out, err := cmd.CombinedOutput()
	line := strings.TrimSpace(string(out))
	if line != "" {
		log("%s %s → %s", name, strings.Join(args, " "), line)
	} else {
		log("%s %s", name, strings.Join(args, " "))
	}
	if err != nil {
		if line == "" {
			line = err.Error()
		}
		return fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), line)
	}
	return nil
}

func runSQLiteSchema(ctx context.Context, dbPath string) error {
	schema := powerDNSSQLiteSchema
	for _, path := range []string{"/usr/share/doc/pdns-backend-sqlite3/schema.sqlite3.sql", "/usr/share/pdns-backend-sqlite3/schema.sqlite3.sql"} {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			schema = string(b)
			break
		}
	}
	cmdPath, err := lookupCommand("sqlite3")
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, cmdPath, dbPath)
	cmd.Stdin = strings.NewReader(schema)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sqlite3 schema import failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func lookupCommand(name string) (string, error) {
	if strings.Contains(name, "/") {
		if _, err := os.Stat(name); err == nil {
			return name, nil
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	for _, prefix := range []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		candidate := filepath.Join(prefix, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s was not found", name)
}

func normalizeDNSZoneSpec(spec DNSZoneSpec) DNSZoneSpec {
	spec.Domain = strings.Trim(strings.ToLower(strings.TrimSpace(spec.Domain)), ".")
	spec.Nameserver1 = strings.Trim(strings.ToLower(strings.TrimSpace(spec.Nameserver1)), ".")
	spec.Nameserver2 = strings.Trim(strings.ToLower(strings.TrimSpace(spec.Nameserver2)), ".")
	for i := range spec.Records {
		spec.Records[i] = normalizeDNSRecord(spec.Domain, spec.Records[i])
	}
	return spec
}

func normalizeDNSRecord(zone string, record DNSRecordSpec) DNSRecordSpec {
	record.Type = strings.ToUpper(strings.TrimSpace(record.Type))
	record.Name = strings.Trim(strings.ToLower(strings.TrimSpace(record.Name)), ".")
	if record.Name == "@" || record.Name == "" {
		record.Name = zone
	} else if !strings.HasSuffix(record.Name, "."+zone) && record.Name != zone {
		record.Name = record.Name + "." + zone
	}
	record.Content = strings.TrimSpace(record.Content)
	if record.TTL <= 0 {
		record.TTL = 300
	}
	if record.Type == "CNAME" || record.Type == "NS" || record.Type == "MX" {
		parts := strings.Fields(record.Content)
		if record.Type == "MX" && len(parts) == 2 {
			record.Content = parts[0] + " " + ensureTrailingDot(parts[1])
		} else if record.Type != "MX" {
			record.Content = ensureTrailingDot(record.Content)
		}
	}
	return record
}

func ensureTrailingDot(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasSuffix(value, ".") {
		return value
	}
	return value + "."
}

func dnsRoleRecovery(id string) string {
	switch id {
	case "validate":
		return "Run the Cloudrifts installer again so pdns-server, pdns-backend-sqlite3, sqlite3, and systemd are available."
	case "database":
		return "Check /var/lib/powerdns ownership and disk space, then retry DNS setup."
	case "config":
		return "Check /etc/powerdns permissions, then retry DNS setup."
	case "service":
		return "Open the job log for the exact PowerDNS startup error, fix it, then retry DNS setup."
	case "firewall":
		return "Open port 53/tcp and 53/udp manually if UFW blocked the automatic rule."
	default:
		return "Fix the reported DNS setup issue and retry."
	}
}

func dnsZoneRecovery(id string) string {
	switch id {
	case "validate":
		return "Run DNS role setup first, then retry zone creation."
	case "zone":
		return "Check PowerDNS database access and retry creating the zone."
	case "records":
		return "Fix the record value shown in the job log, then retry applying DNS records."
	case "reload":
		return "Check PowerDNS status, then retry after the service is healthy."
	default:
		return "Fix the reported DNS zone issue and retry."
	}
}

const powerDNSSQLiteSchema = `
CREATE TABLE IF NOT EXISTS domains (
  id                    INTEGER PRIMARY KEY,
  name                  VARCHAR(255) NOT NULL COLLATE NOCASE,
  master                VARCHAR(128) DEFAULT NULL,
  last_check            INTEGER DEFAULT NULL,
  type                  VARCHAR(8) NOT NULL,
  notified_serial       INTEGER DEFAULT NULL,
  account               VARCHAR(40) DEFAULT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS name_index ON domains(name);
CREATE TABLE IF NOT EXISTS records (
  id                    INTEGER PRIMARY KEY,
  domain_id             INTEGER DEFAULT NULL,
  name                  VARCHAR(255) DEFAULT NULL,
  type                  VARCHAR(10) DEFAULT NULL,
  content               VARCHAR(65535) DEFAULT NULL,
  ttl                   INTEGER DEFAULT NULL,
  prio                  INTEGER DEFAULT NULL,
  disabled              BOOLEAN DEFAULT 0,
  ordername             VARCHAR(255),
  auth                  BOOL DEFAULT 1,
  FOREIGN KEY(domain_id) REFERENCES domains(id) ON DELETE CASCADE ON UPDATE CASCADE
);
CREATE INDEX IF NOT EXISTS rec_name_index ON records(name);
CREATE INDEX IF NOT EXISTS nametype_index ON records(name,type);
CREATE INDEX IF NOT EXISTS domain_id ON records(domain_id);
CREATE TABLE IF NOT EXISTS supermasters (ip VARCHAR(64) NOT NULL, nameserver VARCHAR(255) NOT NULL, account VARCHAR(40) NOT NULL, PRIMARY KEY(ip, nameserver));
CREATE TABLE IF NOT EXISTS comments (id INTEGER PRIMARY KEY, domain_id INTEGER NOT NULL, name VARCHAR(255) NOT NULL, type VARCHAR(10) NOT NULL, modified_at INTEGER NOT NULL, account VARCHAR(40) DEFAULT NULL, comment VARCHAR(65535) NOT NULL, FOREIGN KEY(domain_id) REFERENCES domains(id) ON DELETE CASCADE ON UPDATE CASCADE);
CREATE INDEX IF NOT EXISTS comments_domain_id_idx ON comments(domain_id);
CREATE TABLE IF NOT EXISTS domainmetadata (id INTEGER PRIMARY KEY, domain_id INTEGER NOT NULL, kind VARCHAR(32), content TEXT, FOREIGN KEY(domain_id) REFERENCES domains(id) ON DELETE CASCADE ON UPDATE CASCADE);
CREATE INDEX IF NOT EXISTS domainmetadata_idx ON domainmetadata(domain_id, kind);
CREATE TABLE IF NOT EXISTS cryptokeys (id INTEGER PRIMARY KEY, domain_id INTEGER NOT NULL, flags INTEGER NOT NULL, active BOOL, published BOOL DEFAULT 1, content TEXT, FOREIGN KEY(domain_id) REFERENCES domains(id) ON DELETE CASCADE ON UPDATE CASCADE);
CREATE INDEX IF NOT EXISTS cryptokeys_idx ON cryptokeys(domain_id);
CREATE TABLE IF NOT EXISTS tsigkeys (id INTEGER PRIMARY KEY, name VARCHAR(255), algorithm VARCHAR(50), secret VARCHAR(255));
CREATE UNIQUE INDEX IF NOT EXISTS namealgoindex ON tsigkeys(name, algorithm);
`
