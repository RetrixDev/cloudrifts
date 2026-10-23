export type SetupSettings = {
  panel_url: string;
  webmail_url: string;
  phpmyadmin_url: string;
  dns_mode: 'external' | 'managed';
  nameserver_1: string;
  nameserver_2: string;
  webserver: 'nginx' | 'apache' | 'openlitespeed' | 'litespeed';
  database: 'mariadb' | 'mysql' | 'postgresql';
  email_enabled: boolean;
  backup_path: string;
  panel_ssl_status: 'not_configured' | 'pending' | 'active' | 'needs_dns' | 'self_signed' | 'failed';
  panel_ssl_reason?: string;
  last_setup_job_id?: string;
  managed_dns_zone?: string;
  setup_completed: boolean;
  updated_at?: string;
};


export type DelegationRecord = { type: string; name: string; value: string; where: string; status: string; detail: string };
export type SetupApplyResponse = { settings: SetupSettings; job: Job; role: DNSRoleState; zone?: DNSZone; delegation_records: DelegationRecord[] };

export type SetupState = { completed: boolean; done: number; total: number; steps: { id: string; title: string; done: boolean; detail?: string }[] };
export type SetupSSOResponse = { ok: boolean; expires_at: string };
export type SetupDNSCheck = { panel_url: string; root_domain: string; server_ip: string; dns_mode: 'external' | 'managed'; status: 'waiting' | 'verified' | 'partial' | 'action_required'; title: string; detail: string; panel_resolved: string[]; nameservers: string[]; panel_record_ok: boolean; delegation_ok: boolean; nameserver_a_ok: boolean; required_records: DelegationRecord[]; checked_at: string };

export type WebsiteApp = 'wordpress' | 'woocommerce' | 'php';
export type WebsiteStatus = 'provisioning' | 'active' | 'attention' | 'failed';

export type DNSRecord = { type: string; name: string; value: string; status: string; reason?: string };
export type DNSZoneRecord = { id: string; type: string; name: string; value: string; ttl: number; status: string; reason?: string; created_at: string; updated_at: string };
export type DNSZone = {
  id: string;
  domain: string;
  mode: string;
  status: string;
  server_ip: string;
  nameserver_1: string;
  nameserver_2: string;
  records: DNSZoneRecord[];
  issues: WebsiteIssue[];
  last_job_id?: string;
  last_applied_at?: string;
  created_at: string;
  updated_at: string;
};
export type DNSRoleState = { mode: string; status: string; installed: boolean; ready: boolean; server_ip: string; nameserver_1: string; nameserver_2: string; updated_at: string };
export type MailRoleState = { enabled: boolean; status: string; installed: boolean; ready: boolean; hostname: string; webmail_url: string; server_ip: string; updated_at: string };
export type WebsiteIssue = { code: string; severity: string; title: string; detail: string; fix: string; retryable: boolean };
export type SSLState = { provider: string; status: string; reason?: string; can_retry: boolean; retry_count: number; last_checked_at?: string };
export type WebsiteAction = { id: string; label: string; kind: string; enabled: boolean };
export type JobStatus = 'queued' | 'running' | 'done' | 'failed';

export type PanelWebsite = {
  id: string;
  domain: string;
  app: WebsiteApp;
  kind: string;
  document_root: string;
  php_version: string;
  storage_bytes: number;
  healthy: boolean;
  dns_status: string;
  ssl_status: string;
  ssl: SSLState;
  required_records: DNSRecord[];
  issues: WebsiteIssue[];
  next_actions: WebsiteAction[];
  last_job_id?: string;
  status: WebsiteStatus;
  created_at: string;
  updated_at: string;
};

export type JobStep = {
  id: string;
  title: string;
  detail: string;
  status: 'pending' | 'running' | 'done' | 'warning' | 'failed';
  issues?: WebsiteIssue[];
  started_at?: string;
  ended_at?: string;
};

export type Job = {
  id: string;
  type: string;
  title: string;
  target_id?: string;
  target_name?: string;
  status: JobStatus;
  steps: JobStep[];
  issues: WebsiteIssue[];
  logs: string[];
  created_at: string;
  updated_at: string;
};

export type ActivityEvent = {
  id: string;
  icon: string;
  title: string;
  detail: string;
  tone: string;
  created_at: string;
};

export type ServerRole = {
  id: string;
  name: string;
  kind: string;
  version: string;
  status: string;
  description: string;
  updated_at: string;
};

export type SetupStep = {
  id: string;
  label: string;
  done: boolean;
  action: string;
};

export type ServerSummary = {
  name: string;
  hostname: string;
  os: string;
  region: string;
  ipv4: string;
  mode: string;
  webserver: string;
  database: string;
  dns_mode: string;
  backup_path: string;
};

export type PanelOverview = {
  server: ServerSummary;
  websites: PanelWebsite[];
  jobs: Job[];
  activity: ActivityEvent[];
  roles: ServerRole[];
  setup_steps: SetupStep[];
};

const API_BASE = import.meta.env.VITE_API_BASE ?? (typeof window !== 'undefined' && window.location.port === '5173' ? 'http://localhost:8080' : typeof window !== 'undefined' ? window.location.origin : 'http://localhost:8080');

async function readJSON<T>(res: Response, fallback: string): Promise<T> {
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error ?? fallback);
  }
  return res.json();
}


export async function getSetupState(): Promise<SetupState> {
  const res = await fetch(`${API_BASE}/api/setup/state`);
  return readJSON<SetupState>(res, 'Failed to load setup state');
}

export async function validateSetupSSO(token: string): Promise<SetupSSOResponse> {
  const res = await fetch(`${API_BASE}/api/setup/sso/${encodeURIComponent(token)}`);
  return readJSON<SetupSSOResponse>(res, 'Invalid setup login link');
}

export async function getSetupSettings(): Promise<SetupSettings> {
  const res = await fetch(`${API_BASE}/api/setup/settings`);
  return readJSON<SetupSettings>(res, 'Failed to load setup settings');
}

export async function saveSetupSettings(settings: SetupSettings): Promise<SetupSettings> {
  const res = await fetch(`${API_BASE}/api/setup/settings`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  });
  return readJSON<SetupSettings>(res, 'Failed to save setup settings');
}

export async function getSuggestions(panelUrl: string): Promise<Partial<SetupSettings>> {
  const res = await fetch(`${API_BASE}/api/setup/suggestions?panel_url=${encodeURIComponent(panelUrl)}`);
  return readJSON<Partial<SetupSettings>>(res, 'Failed to load suggestions');
}

export async function checkSetupDNS(panelUrl: string, dnsMode: SetupSettings['dns_mode']): Promise<SetupDNSCheck> {
  const params = new URLSearchParams({ panel_url: panelUrl, dns_mode: dnsMode });
  const res = await fetch(`${API_BASE}/api/setup/dns-check?${params.toString()}`);
  return readJSON<SetupDNSCheck>(res, 'Failed to check DNS');
}

export async function applySetup(settings: SetupSettings): Promise<SetupApplyResponse> {
  const res = await fetch(`${API_BASE}/api/setup/apply`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  });
  return readJSON<SetupApplyResponse>(res, 'Failed to apply setup');
}

export async function getPanelOverview(): Promise<PanelOverview> {
  const res = await fetch(`${API_BASE}/api/panel/overview`);
  return readJSON<PanelOverview>(res, 'Failed to load panel overview');
}

export async function getWebsites(): Promise<PanelWebsite[]> {
  const res = await fetch(`${API_BASE}/api/websites`);
  const body = await readJSON<{ websites: PanelWebsite[] }>(res, 'Failed to load websites');
  return body.websites;
}

export async function createWebsite(input: { domain: string; app: WebsiteApp; php_version?: string }): Promise<{ website: PanelWebsite; job: Job }> {
  const res = await fetch(`${API_BASE}/api/websites`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  return readJSON<{ website: PanelWebsite; job: Job }>(res, 'Failed to create website');
}

export async function getJobs(): Promise<Job[]> {
  const res = await fetch(`${API_BASE}/api/jobs`);
  const body = await readJSON<{ jobs: Job[] }>(res, 'Failed to load jobs');
  return body.jobs;
}

export async function getJob(id: string): Promise<Job> {
  const res = await fetch(`${API_BASE}/api/jobs/${encodeURIComponent(id)}`);
  return readJSON<Job>(res, 'Failed to load job');
}


export async function retryWebsiteSSL(id: string): Promise<{ website: PanelWebsite; job: Job }> {
  const res = await fetch(`${API_BASE}/api/websites/${encodeURIComponent(id)}/ssl/retry`, { method: 'POST' });
  return readJSON<{ website: PanelWebsite; job: Job }>(res, 'Failed to retry SSL');
}

export async function getWebsiteDiagnostics(id: string): Promise<{ website: PanelWebsite; checks: Array<{ id: string; title: string; status: string; detail: string; fix?: string; evidence?: string }>; required_records: DNSRecord[]; can_retry_ssl: boolean }> {
  const res = await fetch(`${API_BASE}/api/websites/${encodeURIComponent(id)}/diagnostics`);
  return readJSON(res, 'Failed to load website diagnostics');
}

export type Backup = {
  id: string;
  website_id: string;
  website_domain: string;
  type: string;
  status: string;
  path?: string;
  size_bytes: number;
  includes_files: boolean;
  includes_database: boolean;
  job_id?: string;
  last_restore_job_id?: string;
  issues: WebsiteIssue[];
  logs: string[];
  created_at: string;
  completed_at?: string;
  updated_at: string;
};

export type FileEntry = { name: string; path: string; type: 'file' | 'directory'; size: number; mode: string; modified_at: string };
export type FileList = { website: PanelWebsite; path: string; entries: FileEntry[] };
export type FileRead = { website: PanelWebsite; path: string; size: number; content: string };
export type WebsiteLogs = { website: PanelWebsite; kind: string; lines: string[]; path?: string };

export async function getWebsiteBackups(id: string): Promise<Backup[]> {
  const res = await fetch(`${API_BASE}/api/websites/${encodeURIComponent(id)}/backups`);
  const body = await readJSON<{ backups: Backup[] }>(res, 'Failed to load website backups');
  return body.backups;
}

export async function createWebsiteBackup(id: string): Promise<{ backup: Backup; job: Job }> {
  const res = await fetch(`${API_BASE}/api/websites/${encodeURIComponent(id)}/backups`, { method: 'POST' });
  return readJSON<{ backup: Backup; job: Job }>(res, 'Failed to create website backup');
}

export async function restoreWebsiteBackup(id: string, backupId: string): Promise<{ backup: Backup; job: Job }> {
  const res = await fetch(`${API_BASE}/api/websites/${encodeURIComponent(id)}/backups/${encodeURIComponent(backupId)}/restore`, { method: 'POST' });
  return readJSON<{ backup: Backup; job: Job }>(res, 'Failed to restore website backup');
}

export async function listWebsiteFiles(id: string, path = '/'): Promise<FileList> {
  const res = await fetch(`${API_BASE}/api/websites/${encodeURIComponent(id)}/files?path=${encodeURIComponent(path)}`);
  return readJSON<FileList>(res, 'Failed to list website files');
}

export async function readWebsiteFile(id: string, path: string): Promise<FileRead> {
  const res = await fetch(`${API_BASE}/api/websites/${encodeURIComponent(id)}/files/read?path=${encodeURIComponent(path)}`);
  return readJSON<FileRead>(res, 'Failed to read website file');
}

export async function writeWebsiteFile(id: string, path: string, content: string): Promise<FileRead> {
  const res = await fetch(`${API_BASE}/api/websites/${encodeURIComponent(id)}/files/write`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path, content }),
  });
  return readJSON<FileRead>(res, 'Failed to write website file');
}

export async function getWebsiteLogs(id: string, kind = 'error', lines = 200): Promise<WebsiteLogs> {
  const res = await fetch(`${API_BASE}/api/websites/${encodeURIComponent(id)}/logs?kind=${encodeURIComponent(kind)}&lines=${encodeURIComponent(String(lines))}`);
  return readJSON<WebsiteLogs>(res, 'Failed to load website logs');
}

export type WebserverOption = {
  id: 'nginx' | 'apache' | 'openlitespeed' | 'litespeed';
  name: string;
  default: boolean;
  current: boolean;
  installed: boolean;
  status: string;
  description: string;
};

export type ServerStats = {
  collected_at: string;
  cpu_count: number;
  load_avg?: string;
  memory_kb: Record<string, number>;
};

export async function getWebservers(): Promise<WebserverOption[]> {
  const res = await fetch(`${API_BASE}/api/server/webservers`);
  const body = await readJSON<{ webservers: WebserverOption[] }>(res, 'Failed to load webservers');
  return body.webservers;
}

export async function getServerStats(): Promise<ServerStats> {
  const res = await fetch(`${API_BASE}/api/server/stats`);
  return readJSON<ServerStats>(res, 'Failed to load server stats');
}

export async function switchWebserver(webserver: WebserverOption['id']): Promise<{ from: string; to: string; job: Job; webservers: WebserverOption[] }> {
  const res = await fetch(`${API_BASE}/api/server/webservers/switch`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ webserver }),
  });
  return readJSON<{ from: string; to: string; job: Job; webservers: WebserverOption[] }>(res, 'Failed to switch webserver');
}

export async function getDNSZones(): Promise<{ role: DNSRoleState; zones: DNSZone[] }> {
  const res = await fetch(`${API_BASE}/api/dns/zones`);
  return readJSON<{ role: DNSRoleState; zones: DNSZone[] }>(res, 'Failed to load DNS zones');
}

export async function setupDNSRole(): Promise<{ role: DNSRoleState; job: Job }> {
  const res = await fetch(`${API_BASE}/api/dns/setup`, { method: 'POST' });
  return readJSON<{ role: DNSRoleState; job: Job }>(res, 'Failed to setup DNS role');
}

export async function createDNSZone(input: { domain: string; records?: Partial<DNSZoneRecord>[] }): Promise<{ zone: DNSZone; job: Job }> {
  const res = await fetch(`${API_BASE}/api/dns/zones`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  return readJSON<{ zone: DNSZone; job: Job }>(res, 'Failed to create DNS zone');
}

export async function applyDNSZone(id: string): Promise<{ zone: DNSZone; job: Job }> {
  const res = await fetch(`${API_BASE}/api/dns/zones/${encodeURIComponent(id)}/apply`, { method: 'POST' });
  return readJSON<{ zone: DNSZone; job: Job }>(res, 'Failed to apply DNS zone');
}

export async function upsertDNSRecord(id: string, input: { type: string; name: string; value: string; ttl: number }): Promise<{ zone: DNSZone; record: DNSZoneRecord; job: Job }> {
  const res = await fetch(`${API_BASE}/api/dns/zones/${encodeURIComponent(id)}/records`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  return readJSON<{ zone: DNSZone; record: DNSZoneRecord; job: Job }>(res, 'Failed to update DNS record');
}

export async function getMailRole(): Promise<MailRoleState> {
  const res = await fetch(`${API_BASE}/api/mail/role`);
  return readJSON<MailRoleState>(res, 'Failed to load mail role');
}

export async function setupMailRole(): Promise<{ role: MailRoleState; job: Job }> {
  const res = await fetch(`${API_BASE}/api/mail/setup`, { method: 'POST' });
  return readJSON<{ role: MailRoleState; job: Job }>(res, 'Failed to setup mail role');
}
