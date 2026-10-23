import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { CloudriftsMark, Icon, type IconName } from './components/Icon';
import { RiftSignature, ServerIllustration, SiteThumbnail, type MetricName } from './components/Visuals';
import {
  applyDNSZone,
  applySetup,
  checkSetupDNS,
  createDNSZone,
  createWebsite,
  createWebsiteBackup,
  getDNSZones,
  getJob,
  getMailRole,
  getPanelOverview,
  getSetupSettings,
  getSetupState,
  getSuggestions,
  getServerStats,
  getWebservers,
  getWebsiteBackups,
  getWebsiteLogs,
  listWebsiteFiles,
  readWebsiteFile,
  restoreWebsiteBackup,
  retryWebsiteSSL,
  saveSetupSettings,
  setupDNSRole,
  setupMailRole,
  switchWebserver,
  upsertDNSRecord,
  validateSetupSSO,
  writeWebsiteFile,
  type ActivityEvent,
  type Backup,
  type DelegationRecord,
  type DNSRecord,
  type DNSRoleState,
  type DNSZone,
  type FileEntry,
  type Job,
  type MailRoleState,
  type PanelOverview,
  type PanelWebsite,
  type ServerRole,
  type ServerStats,
  type ServerSummary,
  type SetupDNSCheck,
  type SetupSettings,
  type SetupState,
  type WebsiteApp,
  type WebsiteIssue,
  type WebserverOption,
} from './api';
import './styles.css';

type Page = 'Overview' | 'Websites' | 'Server' | 'Activity' | 'Team' | 'Settings';
type Site = { id: string; domain: string; kind: string; app: 'WordPress' | 'WooCommerce' | 'PHP'; storage: string; healthy: boolean; dnsStatus?: string; sslStatus?: string; sslReason?: string; documentRoot?: string; phpVersion?: string; requiredRecords?: DNSRecord[]; issues?: WebsiteIssue[]; lastJobId?: string; status?: string };
type Drawer = { type: 'create' } | { type: 'setup' } | { type: 'certificates' } | { type: 'backups' } | { type: 'server' } | { type: 'help' } | { type: 'account' } | { type: 'site'; site: Site } | null;
type UIEvent = { icon: IconName; title: string; detail: string; time: string; tone: string };
type TrackedJob = Job & { seen?: boolean; registered_at?: string };
type AccessMode = 'checking' | 'login' | 'setup' | 'app';

const nav: { label: Page; icon: IconName }[] = [
  { label: 'Overview', icon: 'dashboard' },
  { label: 'Websites', icon: 'globe' },
  { label: 'Server', icon: 'server' },
  { label: 'Activity', icon: 'activity' },
  { label: 'Team', icon: 'team' },
  { label: 'Settings', icon: 'settings' },
];

const savedTheme = (() => { try { return localStorage.getItem('cloudrifts-appearance') === 'dark'; } catch { return false; } })();
const appFromAPI = (app: WebsiteApp): Site['app'] => app === 'woocommerce' ? 'WooCommerce' : app === 'wordpress' ? 'WordPress' : 'PHP';
const appToAPI = (app: Site['app']): WebsiteApp => app === 'WooCommerce' ? 'woocommerce' : app === 'WordPress' ? 'wordpress' : 'php';
const formatBytes = (bytes: number) => { if (!bytes) return '0 MB'; const units = ['B', 'KB', 'MB', 'GB', 'TB']; let value = bytes; let unit = 0; while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; } return `${value >= 10 || unit < 2 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`; };
const siteFromAPI = (site: PanelWebsite): Site => ({ id: site.id, domain: site.domain, kind: site.kind || 'site', app: appFromAPI(site.app), storage: formatBytes(site.storage_bytes), healthy: site.healthy && site.status !== 'failed', dnsStatus: site.dns_status, sslStatus: site.ssl_status, sslReason: site.ssl?.reason, documentRoot: site.document_root, phpVersion: site.php_version, requiredRecords: site.required_records || [], issues: site.issues || [], lastJobId: site.last_job_id, status: site.status });
const relativeTime = (value: string) => { const date = new Date(value); if (Number.isNaN(date.getTime())) return 'Just now'; const diff = Date.now() - date.getTime(); const minute = 60_000, hour = 60 * minute, day = 24 * hour; if (diff < minute) return 'Just now'; if (diff < hour) return `${Math.max(1, Math.round(diff / minute))}m ago`; if (diff < day) return `${Math.max(1, Math.round(diff / hour))}h ago`; return date.toLocaleDateString(undefined, { day: '2-digit', month: 'short' }); };
const eventFromAPI = (event: ActivityEvent): UIEvent => ({ icon: ((['backup', 'shield', 'globe', 'server', 'settings', 'database'] as string[]).includes(event.icon) ? event.icon : 'activity') as IconName, title: event.title, detail: event.detail, time: relativeTime(event.created_at), tone: event.tone || 'violet' });
const delay = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));
const webserverLabel = (id?: string) => id === 'apache' ? 'Apache' : id === 'openlitespeed' ? 'OpenLiteSpeed' : id === 'litespeed' ? 'LiteSpeed' : 'Nginx';
const normalizeSetupHost = (value: string) => value.trim().toLowerCase().replace(/^https?:\/\//, '').replace(/\/$/, '').replace(/\.$/, '');
const setupRootDomain = (host: string) => { const parts = normalizeSetupHost(host).split('.').filter(Boolean); return parts.length > 1 ? parts.slice(-2).join('.') : 'your-domain.com'; };
const setupDerivedURLs = (panelURL: string) => { const root = setupRootDomain(panelURL); return { webmail_url: `webmail.${root}`, phpmyadmin_url: `phpmyadmin.${root}`, nameserver_1: `ns1.${root}`, nameserver_2: `ns2.${root}` }; };
const isDerivedSetupValue = (value: string, key: keyof ReturnType<typeof setupDerivedURLs>, panelURL: string) => !value || value === setupDerivedURLs(panelURL)[key];
const jobNotificationEvent = 'cloudrifts:track-job';
const trackPanelJob = (job: Job, title?: string) => {
  window.dispatchEvent(new CustomEvent(jobNotificationEvent, { detail: { job: title ? { ...job, title } : job } }));
};
const jobProgress = (job: Job) => {
  const total = job.steps?.length || 1;
  const done = job.steps?.filter(step => step.status === 'done' || step.status === 'warning' || step.status === 'failed').length || 0;
  return Math.max(job.status === 'done' || job.status === 'failed' ? 100 : 8, Math.round((done / total) * 100));
};
const jobTone = (job: Job) => job.status === 'failed' ? 'attention' : job.status === 'done' ? 'healthy' : 'running';
const jobStatusLabel = (job: Job) => job.status === 'queued' ? 'Queued' : job.status === 'running' ? 'Running' : job.status === 'done' ? 'Completed' : 'Failed';

const setupSessionKey = 'cloudrifts-setup-session';
const setupMaxStepKey = 'cloudrifts-setup-max-step';
const setupSSOTokenFromPath = () => {
  const match = window.location.pathname.match(/^\/setup\/sso\/([^/?#]+)/);
  return match ? decodeURIComponent(match[1]) : '';
};

function AuthFrame({ title, detail, state }: { title: string; detail: string; state?: SetupState | null }) {
  const done = state?.done ?? 0;
  const total = state?.total ?? 4;
  return <main className="auth-screen"><section className="auth-card is-loading"><div className="auth-brand"><CloudriftsMark /><span>cloudrifts<span className="brand-period">.</span></span></div><div className="auth-orbit" aria-hidden="true"><i /><i /><i /></div><span className="brand-chip"><CloudriftsMark /> installer handoff</span><h1>{title}</h1><p>{detail}</p><div className="auth-progress"><span style={{ width: `${Math.max(8, Math.min(100, total ? (done / total) * 100 : 8))}%` }} /></div><small>{done}/{total} setup checks ready</small></section></main>;
}

function LoginGate({ error, state }: { error: string; state: SetupState | null }) {
  const setupUrlExample = `${window.location.origin}/setup/sso/<token>`;
  return <main className="auth-screen"><section className="auth-card"><div className="auth-brand"><CloudriftsMark /><span>cloudrifts<span className="brand-period">.</span></span></div><span className="brand-chip"><Icon name="lock" size={14} /> setup login required</span><h1>Open the installer SSO link</h1><p>Direct panel access is locked until first-run setup is opened from a one-time SSO link generated by the Cloudrifts command center.</p><div className="login-url-card"><span>Expected URL</span><code>{setupUrlExample}</code></div>{error && <div className="inline-alert"><Icon name="warning" size={18} /><div><strong>Login link not accepted</strong><span>{error}</span></div></div>}<div className="setup-mini-steps">{(state?.steps || []).slice(0, 4).map(step => <span key={step.id} className={step.done ? 'done' : ''}><i />{step.title}</span>)}</div><button className="button secondary full-button" onClick={() => window.location.reload()}><Icon name="activity" size={16} />Check again</button><p className="form-note compact-note"><Icon name="shield" size={16} /><span>Run <code>cloudrifts sso</code> on the server for a fresh setup/login link.</span></p></section></main>;
}

function SetupExperience({ server, state, refresh, notify, onSetupApplied }: { server: ServerSummary | null; state: SetupState | null; refresh: () => Promise<PanelOverview>; notify: (message: string) => void; onSetupApplied: () => Promise<void> }) {
  const done = state?.done ?? 0;
  const total = state?.total ?? 4;
  return <main className="setup-screen"><section className="setup-shell setup-shell-v2"><div className="setup-shell-hero compact"><div className="setup-brand-line"><div className="auth-brand"><CloudriftsMark /><span>cloudrifts<span className="brand-period">.</span></span></div><span className="brand-chip"><Icon name="settings" size={14} /> guided setup</span></div><div className="setup-hero-status"><span><Icon name="globe" size={15} />{server?.ipv4 || 'server ip'}</span><span><Icon name="shield" size={15} />SSL ready flow</span><span><Icon name="network" size={15} />DNS guided</span></div><div className="setup-progress-card orb"><strong>{done}/{total}</strong><span>ready</span><i style={{ width: `${Math.max(12, Math.min(100, total ? (done / total) * 100 : 12))}%` }} /></div></div><div className="setup-main-card"><SetupDrawer server={server} refresh={refresh} notify={notify} onSetupApplied={onSetupApplied} /></div></section></main>;
}

function App() {
  const [page, setPage] = useState<Page>('Overview');
  const [drawer, setDrawer] = useState<Drawer>(null);
  const [overview, setOverview] = useState<PanelOverview | null>(null);
  const [webservers, setWebservers] = useState<WebserverOption[]>([]);
  const [dnsRole, setDNSRole] = useState<DNSRoleState | null>(null);
  const [dnsZones, setDNSZones] = useState<DNSZone[]>([]);
  const [mailRole, setMailRole] = useState<MailRoleState | null>(null);
  const [stats, setStats] = useState<ServerStats | null>(null);
  const [statsHistory, setStatsHistory] = useState<ServerStats[]>([]);
  const [apiConnected, setApiConnected] = useState(false);
  const [loading, setLoading] = useState(true);
  const [toast, setToast] = useState('');
  const [lastError, setLastError] = useState('');
  const [filter, setFilter] = useState('All websites');
  const [siteSearch, setSiteSearch] = useState('');
  const [metric, setMetric] = useState<MetricName>('CPU');
  const [range, setRange] = useState('1h');
  const [dark, setDark] = useState(savedTheme);
  const [command, setCommand] = useState(false);
  const [query, setQuery] = useState('');
  const [notifications, setNotifications] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [accessMode, setAccessMode] = useState<AccessMode>('checking');
  const [setupState, setSetupState] = useState<SetupState | null>(null);
  const [setupAuthError, setSetupAuthError] = useState('');
  const [jobNotifications, setJobNotifications] = useState<TrackedJob[]>([]);

  const notify = useCallback((message: string) => setToast(message), []);
  const registerJob = useCallback((job: Job) => {
    setJobNotifications(prev => {
      const next: TrackedJob = { ...job, registered_at: new Date().toISOString(), seen: false };
      const exists = prev.some(item => item.id === job.id);
      const merged = exists ? prev.map(item => item.id === job.id ? { ...item, ...next, seen: item.seen && item.status === next.status } : item) : [next, ...prev];
      return merged.slice(0, 12);
    });
  }, []);
  const refresh = useCallback(async () => {
    const [overviewResult, webserversResult, statsResult, dnsResult, mailResult] = await Promise.all([getPanelOverview(), getWebservers(), getServerStats().catch(() => null), getDNSZones().catch(() => null), getMailRole().catch(() => null)]);
    setOverview(overviewResult);
    setWebservers(webserversResult);
    setStats(statsResult);
    if (dnsResult) {
      setDNSRole(dnsResult.role);
      setDNSZones(dnsResult.zones);
    }
    if (mailResult) setMailRole(mailResult);
    if (statsResult) setStatsHistory(prev => [...prev.slice(-59), statsResult]);
    setApiConnected(true);
    setLastError('');
    setLoading(false);
    return overviewResult;
  }, []);

  useEffect(() => { document.documentElement.dataset.theme = dark ? 'dark' : 'light'; try { localStorage.setItem('cloudrifts-appearance', dark ? 'dark' : 'light'); } catch { /* ignore */ } }, [dark]);
  useEffect(() => { const handler = (event: KeyboardEvent) => { if ((event.metaKey || event.ctrlKey) && event.key === 'k') { event.preventDefault(); setCommand(v => !v); setQuery(''); } if (event.key === 'Escape') { setDrawer(null); setCommand(false); setNotifications(false); setMenuOpen(false); } }; window.addEventListener('keydown', handler); return () => window.removeEventListener('keydown', handler); }, []);
  useEffect(() => {
    let cancelled = false;
    const boot = async () => {
      setAccessMode('checking');
      setSetupAuthError('');
      try {
        const state = await getSetupState();
        if (cancelled) return;
        setSetupState(state);
        const token = setupSSOTokenFromPath();
        if (token) {
          await validateSetupSSO(token);
          if (cancelled) return;
          try { sessionStorage.setItem(setupSessionKey, token); } catch { /* ignore */ }
          window.history.replaceState({}, '', '/setup');
          setAccessMode(state.completed ? 'app' : 'setup');
          return;
        }
        if (state.completed) {
          setAccessMode('app');
          return;
        }
        let hasSetupSession = false;
        try { hasSetupSession = Boolean(sessionStorage.getItem(setupSessionKey)); } catch { hasSetupSession = false; }
        setAccessMode(hasSetupSession ? 'setup' : 'login');
      } catch (err) {
        if (cancelled) return;
        setSetupAuthError(err instanceof Error ? err.message : 'Could not contact Cloudrifts backend');
        setAccessMode('login');
      }
    };
    boot();
    return () => { cancelled = true; };
  }, []);
  useEffect(() => { if (!toast) return; const timer = setTimeout(() => setToast(''), 3500); return () => clearTimeout(timer); }, [toast]);
  useEffect(() => {
    const handler = (event: Event) => {
      const custom = event as CustomEvent<{ job?: Job }>;
      if (custom.detail?.job) {
        registerJob(custom.detail.job);
        setNotifications(true);
      }
    };
    window.addEventListener(jobNotificationEvent, handler);
    return () => window.removeEventListener(jobNotificationEvent, handler);
  }, [registerJob]);
  useEffect(() => {
    const serverJobs = overview?.jobs || [];
    if (!serverJobs.length) return;
    setJobNotifications(prev => {
      let changed = false;
      const tracked = new Map(prev.map(job => [job.id, job]));
      const candidates = serverJobs.filter(job => job.status === 'queued' || job.status === 'running' || job.status === 'failed').slice(0, 6);
      for (const job of candidates) {
        const existing = tracked.get(job.id);
        if (!existing) {
          tracked.set(job.id, { ...job, registered_at: new Date().toISOString(), seen: job.status !== 'failed' });
          changed = true;
          continue;
        }
        if (existing.status !== job.status || existing.steps.length !== job.steps.length || existing.issues.length !== job.issues.length) {
          tracked.set(job.id, { ...existing, ...job, seen: existing.seen && existing.status === job.status });
          changed = true;
        }
      }
      return changed ? Array.from(tracked.values()).slice(0, 12) : prev;
    });
  }, [overview?.jobs]);

  useEffect(() => {
    const active = jobNotifications.filter(job => job.status === 'queued' || job.status === 'running');
    if (!active.length) return;
    let cancelled = false;
    const poll = async () => {
      const updates = await Promise.all(active.map(job => getJob(job.id).catch(() => null)));
      if (cancelled) return;
      setJobNotifications(prev => prev.map(job => {
        const update = updates.find(item => item?.id === job.id);
        return update ? { ...job, ...update, seen: job.seen && job.status === update.status } : job;
      }));
      for (const update of updates) {
        if (!update || (update.status !== 'done' && update.status !== 'failed')) continue;
        const previous = jobNotifications.find(job => job.id === update.id);
        if (previous && previous.status !== update.status) notify(update.status === 'failed' ? `${update.title} failed` : `${update.title} completed`);
      }
      refresh().catch(() => undefined);
    };
    const timer = setInterval(poll, 1400);
    poll();
    return () => { cancelled = true; clearInterval(timer); };
  }, [jobNotifications, notify, refresh]);
  useEffect(() => {
    if (accessMode !== 'app' && accessMode !== 'setup') return;
    let cancelled = false;
    refresh().catch(err => { if (cancelled) return; setApiConnected(false); setLoading(false); setLastError(err instanceof Error ? err.message : 'Backend is offline'); });
    const interval = setInterval(() => { refresh().catch(() => setApiConnected(false)); }, 8000);
    return () => { cancelled = true; clearInterval(interval); };
  }, [refresh, accessMode]);

  const finishSetupView = async () => {
    const state = await getSetupState().catch(() => null);
    if (state) setSetupState(state);
    if (state?.completed) setAccessMode('app');
    await refresh().catch(() => undefined);
  };

  if (accessMode === 'checking') return <AuthFrame state={setupState} title="Checking setup access" detail="Validating installer SSO and backend state…" />;
  if (accessMode === 'login') return <LoginGate error={setupAuthError} state={setupState} />;
  if (accessMode === 'setup') return <SetupExperience server={overview?.server || null} state={setupState} refresh={refresh} notify={notify} onSetupApplied={finishSetupView} />;

  const sites = useMemo(() => (overview?.websites || []).map(siteFromAPI), [overview]);
  const events = useMemo(() => (overview?.activity || []).map(eventFromAPI), [overview]);
  const jobs = overview?.jobs || [];
  const roles = overview?.roles || [];
  const server = overview?.server || null;
  const healthy = sites.filter(s => s.healthy).length;
  const visibleSites = sites.filter(s => (filter !== 'Needs attention' || !s.healthy) && (filter !== 'WordPress' || s.app === 'WordPress') && s.domain.includes(siteSearch.toLowerCase()));
  const sslIssues = sites.filter(s => (s.issues || []).length || s.sslStatus === 'dns_check_required' || s.sslStatus === 'needs_attention');
  const currentWebserver = webservers.find(w => w.current)?.id || server?.webserver || 'nginx';
  const activeJobNotifications = jobNotifications.filter(job => job.status === 'queued' || job.status === 'running');
  const completedJobNotifications = jobNotifications.filter(job => (job.status === 'done' || job.status === 'failed') && !job.seen);
  const notificationCount = activeJobNotifications.length + completedJobNotifications.length + sslIssues.length;

  const navigate = (next: Page) => { setPage(next); setMenuOpen(false); setSiteSearch(''); setFilter('All websites'); };
  const copy = async (text: string) => { try { await navigator.clipboard.writeText(text); notify('Copied to clipboard'); } catch { notify('Clipboard unavailable. Select and copy the value.'); } };
  const waitForJob = async (jobId: string) => { for (let i = 0; i < 40; i++) { await delay(i < 6 ? 800 : 1600); const job = await getJob(jobId).catch(() => null); await refresh().catch(() => undefined); if (job && (job.status === 'done' || job.status === 'failed')) return job; } return null; };

  const addSite = async (domain: string, app: Site['app']) => {
    notify('Provisioning job queued');
    const created = await createWebsite({ domain, app: appToAPI(app), php_version: '8.3' });
    registerJob(created.job);
    await refresh();
    setDrawer({ type: 'site', site: siteFromAPI(created.website) });
    const finalJob = await waitForJob(created.job.id);
    const updated = await refresh();
    const latest = updated.websites.find(s => s.id === created.website.id);
    if (latest) setDrawer({ type: 'site', site: siteFromAPI(latest) });
    if (finalJob?.status === 'failed') notify('Website failed · exact reason is in the job trail');
    else if (latest?.issues?.length) notify('Website live · SSL needs attention');
    else notify('Website provisioned successfully');
  };

  const retrySSLAction = async (site: Site) => {
    notify('SSL retry queued');
    const result = await retryWebsiteSSL(site.id);
    registerJob(result.job);
    await refresh();
    const finalJob = await waitForJob(result.job.id);
    const updated = await refresh();
    const latest = updated.websites.find(s => s.id === site.id);
    if (latest) setDrawer({ type: 'site', site: siteFromAPI(latest) });
    notify(finalJob?.status === 'failed' ? 'SSL retry failed · check job trail' : latest?.issues?.length ? 'SSL still needs attention · fix shown' : 'SSL is active');
  };

  const switchWebserverAction = async (id: WebserverOption['id']) => {
    notify(`Switching to ${webserverLabel(id)}`);
    const result = await switchWebserver(id);
    registerJob(result.job);
    await refresh();
    const finalJob = await waitForJob(result.job.id);
    await refresh();
    notify(finalJob?.status === 'failed' ? 'Webserver switch failed · exact reason is in Activity' : `Active webserver is now ${webserverLabel(id)}`);
  };

  return <div className="app-shell">
    {menuOpen && <button className="mobile-scrim" aria-label="Close navigation" onClick={() => setMenuOpen(false)} />}
    <aside className={`sidebar ${menuOpen ? 'is-open' : ''}`}>
      <a className="brand" aria-label="Cloudrifts overview" href="#overview" onClick={e => { e.preventDefault(); navigate('Overview'); }}><CloudriftsMark /><span>cloudrifts<span className="brand-period">.</span></span></a>
      <button aria-label="My workspace" className="workspace-switch" onClick={() => setDrawer({ type: 'account' })}><span className="workspace-monogram">C</span><span><strong>My workspace</strong><small>{server?.hostname || 'Local server'}</small></span><Icon name="down" size={14} /></button>
      <div className="nav-caption">WORKSPACE</div>
      <nav aria-label="Main navigation">{nav.map(item => <button key={item.label} aria-label={item.label} title={item.label} className={`nav-item ${page === item.label ? 'is-active' : ''}`} aria-current={page === item.label ? 'page' : undefined} onClick={() => navigate(item.label)}><Icon name={item.icon} /><span>{item.label}</span>{item.label === 'Websites' && <small>{sites.length}</small>}{item.label === 'Server' && <span className="nav-dot" />}</button>)}</nav>
      <div className="sidebar-bottom"><div className="sidebar-health"><span className="status-dot" /><span>{apiConnected ? 'API connected' : 'Backend offline'}</span><span className="connection-bars"><i /><i /><i /><i /></span></div><button aria-label="Documentation" title="Documentation" className="nav-item secondary-nav" onClick={() => setDrawer({ type: 'help' })}><Icon name="book" /><span>Runbook</span><Icon name="upRight" size={14} /></button><button aria-label="Admin account" title="Admin account" className="profile" onClick={() => setDrawer({ type: 'account' })}><span className="avatar">A</span><span><strong>Admin</strong><small>Workspace owner</small></span><Icon name="down" size={14} /></button></div>
    </aside>

    <div className="app-main">
      <header className="topbar">
        <div className="breadcrumbs">
          <button className="icon-button mobile-menu" aria-label="Open navigation" onClick={() => setMenuOpen(true)}><Icon name="menu" /></button>
          <span>Workspace</span>
          <Icon name="chevron" size={12} />
          <strong>{page}</strong>
        </div>
        <div className="topbar-tools">
          <span className={`preview-indicator ${apiConnected ? 'is-live' : ''}`}><i />{apiConnected ? 'Live backend' : 'Backend offline'}</span>
          <button className="search-trigger" onClick={() => { setCommand(true); setQuery(''); }} aria-label="Search workspace"><Icon name="search" size={16} /><span>Search anything...</span><kbd>⌘ K</kbd></button>
          <span className="tool-divider" />
          <button className="icon-button" onClick={() => setDark(v => !v)} aria-label={dark ? 'Switch to light theme' : 'Switch to dark theme'} title={dark ? 'Light theme' : 'Dark theme'}><Icon name={dark ? 'sun' : 'moon'} size={18} /></button>
          <div className="notification-anchor">
            <button className={`icon-button notification-button ${notifications ? 'selected' : ''}`} aria-label="Notifications" aria-expanded={notifications} onClick={() => { setNotifications(v => !v); setJobNotifications(prev => prev.map(job => job.status === 'failed' || job.status === 'done' ? { ...job, seen: true } : job)); }}>
              <Icon name="bell" size={18} />
              {notificationCount > 0 && <i />}
            </button>
            {notifications && <NotificationPopover jobs={jobNotifications} sslIssues={sslIssues} close={() => setNotifications(false)} openSite={site => { setDrawer({ type: 'site', site }); setNotifications(false); }} clearJob={id => setJobNotifications(prev => prev.filter(job => job.id !== id))} />}
          </div>
          <button className="top-avatar" onClick={() => setDrawer({ type: 'account' })} aria-label="Open account">A</button>
        </div>
      </header>

      <main className="workspace-content" id="main-content">
        <div className="page-heading visual-heading"><RiftSignature /><div className="heading-copy"><span className="brand-chip"><CloudriftsMark />Cloudrifts Phase 1</span><h1>{page === 'Overview' ? 'Control room' : page}</h1>{page === 'Overview' && <div className="status-rail" aria-label="Workspace signals"><button onClick={() => setDrawer({ type: 'server' })}><Icon name="server" size={14} /><span>{server?.hostname || 'server'}</span></button><button onClick={() => setDrawer({ type: 'certificates' })}><Icon name="shield" size={14} /><span>{healthy}/{sites.length} SSL</span></button><button onClick={() => setDrawer({ type: 'backups' })}><Icon name="backup" size={14} /><span>{server?.backup_path || '/backups'}</span></button></div>}</div><div className="heading-actions"><button className="button secondary" onClick={() => refresh().then(() => notify('Panel state refreshed')).catch(err => notify(err instanceof Error ? err.message : 'Refresh failed'))}><Icon name="activity" size={16} />Refresh</button><button className="button primary" onClick={() => setDrawer({ type: 'create' })} disabled={!apiConnected}><Icon name="plus" size={16} />New site</button></div></div>

        {!apiConnected && <BackendOffline loading={loading} error={lastError} onRetry={() => refresh().catch(err => setLastError(err instanceof Error ? err.message : 'Backend is offline'))} />}

        {apiConnected && (page === 'Overview' || page === 'Server') && <>
          {page === 'Overview' && <div className="metrics-strip">
            <button className="metric-item" onClick={() => navigate('Websites')}><span className="metric-label">Sites <Icon name="upRight" size={14} /></span><div className="metric-main"><strong>{String(sites.length).padStart(2, '0')}</strong><div className="metric-mini-sites" aria-hidden="true">{sites.slice(0, 3).map(site => <span key={site.id} className={site.kind} />)}</div></div><span className="metric-caption"><i className="status-dot" />{healthy} healthy</span></button>
            <button className="metric-item" onClick={() => setDrawer({ type: 'certificates' })}><span className="metric-label">SSL <Icon name="upRight" size={14} /></span><div className="metric-main"><strong>{healthy}<em>/{sites.length || 0}</em></strong><span className="live-pill">Live</span></div><span className="metric-caption">{sslIssues.length ? `${sslIssues.length} need action` : 'Protected'}</span></button>
            <button className="metric-item" onClick={() => setDrawer({ type: 'backups' })}><span className="metric-label">Backups <Icon name="upRight" size={14} /></span><div className="metric-main"><strong>{server?.backup_path || '/backups'}</strong><div className="backup-stack"><i /><i /><i /><Icon name="check" size={13} /></div></div><span className="metric-caption"><i className="status-dot" />Local path</span></button>
            <button className="metric-item" onClick={() => setDrawer({ type: 'server' })}><span className="metric-label">Webserver <Icon name="upRight" size={14} /></span><div className="metric-main"><strong>{webserverLabel(currentWebserver)}</strong><span className="live-pill">Live</span></div><span className="metric-caption"><i className="status-dot" />Active</span></button>
          </div>}

          <section className="server-console" aria-label="Server overview">
            <div className="server-portrait"><div className="server-portrait-top"><span className="server-type"><Icon name="server" size={14} />YOUR SERVER</span><span className="online-tag"><i />Online</span></div><ServerIllustration /><div className="server-name"><div><h2>{server?.name || 'Master server'}</h2><span><i className="region-mark" />{server?.hostname || 'localhost'} <span className="subtle-dot">·</span> {server?.os || 'Ubuntu 24.04'}</span></div><button className="server-open" aria-label="Manage master server" onClick={() => setDrawer({ type: 'server' })}><Icon name="upRight" size={20} /></button></div></div>
            <div className="server-telemetry"><div className="telemetry-top"><div className="chart-tabs" role="group" aria-label="Server metric">{(['CPU', 'Memory', 'Network'] as MetricName[]).map(t => <button key={t} aria-pressed={metric === t} onClick={() => setMetric(t)} className={metric === t ? 'active' : ''}>{t}</button>)}</div><div className="range-control" aria-label="Chart time range">{['1h', '24h', '7d'].map(t => <button key={t} aria-pressed={range === t} className={range === t ? 'active' : ''} onClick={() => setRange(t)}>{t}</button>)}</div></div><div className="chart-summary"><strong>{metricValue(metric, stats)}<span>{metric === 'Network' ? 'MB/s' : '%'}</span></strong><span className="resource-caption"><i />{metric === 'CPU' ? 'CPU utilization' : metric === 'Memory' ? 'Memory usage' : 'Network throughput'}</span><span className="chart-health"><span className="status-dot" />Live sample</span></div><LiveStatsChart metric={metric} statsHistory={statsHistory} /><div className="resource-footer"><div><Icon name="cpu" size={15} /><span>{stats?.cpu_count || 0} CPU</span><span className="resource-detail">{stats?.load_avg || 'load unavailable'}</span></div><div><Icon name="memory" size={15} /><span>{memoryLabel(stats)}</span><span className="micro-meter"><i style={{ width: `${memoryPercent(stats)}%` }} /></span></div><div><Icon name="globe" size={15} /><span>{webserverLabel(currentWebserver)}</span><span className="resource-detail">active</span></div></div></div>
          </section>
        </>}

        {apiConnected && page === 'Overview' && <div className="dashboard-grid"><section className="sites-panel"><div className="section-heading"><h2>Websites</h2><button className="text-button" onClick={() => navigate('Websites')}>View all<Icon name="arrow" size={14} /></button></div><SiteList sites={sites.slice(0, 6)} open={site => setDrawer({ type: 'site', site })} /></section><aside className="overview-aside"><div className="section-heading"><div><h2>Protection</h2><Icon name="shield" size={16} /></div><button className="icon-button" onClick={() => setDrawer({ type: 'certificates' })} aria-label="Manage protection"><Icon name="upRight" size={16} /></button></div><div className="protection-overview"><div className="protection-ring"><svg viewBox="0 0 80 80"><circle cx="40" cy="40" r="33" /><circle cx="40" cy="40" r="33" strokeDasharray={`${sites.length ? healthy / sites.length * 208 : 0} 208`} /></svg><Icon name="shield" size={25} /><i /></div><div><strong>{healthy} of {sites.length} secured</strong><span>{sslIssues.length ? `${sslIssues.length} need action` : 'Auto SSL healthy'}</span><button onClick={() => setDrawer({ type: 'certificates' })} className="attention-link">Review certificates<Icon name="arrow" size={13} /></button></div></div><div className="activity-heading"><h2>Recent activity</h2><button className="icon-button" aria-label="View all activity" onClick={() => navigate('Activity')}><Icon name="arrow" size={15} /></button></div><ActivityList events={events.slice(0, 5)} /></aside></div>}

        {apiConnected && page === 'Websites' && <section className="route-surface websites-route"><div className="route-toolbar"><div className="site-search"><Icon name="search" /><input value={siteSearch} onChange={e => setSiteSearch(e.target.value)} placeholder="Search websites" /></div><div className="filter-pills">{['All websites', 'Needs attention', 'WordPress'].map(item => <button key={item} className={filter === item ? 'active' : ''} onClick={() => setFilter(item)}>{item}</button>)}</div></div><SiteList sites={visibleSites} open={site => setDrawer({ type: 'site', site })} /></section>}
        {apiConnected && page === 'Activity' && <section className="route-surface"><div className="section-heading"><h2>Workspace activity</h2><span className="soft-label">Real jobs and events</span></div><JobTrail jobs={jobs} /><ActivityList events={events} /></section>}
        {apiConnected && page === 'Server' && <section className="route-surface"><div className="section-heading"><h2>Server roles</h2><span className="soft-label">{roles.filter(r => r.status === 'running' || r.status === 'ready').length} ready</span></div><RoleDetails roles={roles} /><WebserverSwitcher webservers={webservers} onSwitch={switchWebserverAction} /><DNSPanel role={dnsRole} zones={dnsZones} server={server} refresh={refresh} notify={notify} copy={copy} /><MailPanel role={mailRole} refresh={refresh} notify={notify} copy={copy} /><div className="configuration-line"><Icon name="network" /><span>{server?.ipv4 || 'IP unavailable'}</span><button className="text-button" onClick={() => copy(server?.ipv4 || '')}>Copy IP<Icon name="copy" size={14} /></button></div></section>}
        {apiConnected && page === 'Team' && <section className="route-surface"><div className="section-heading"><h2>Workspace members</h2><span className="soft-label">Phase 1 local owner</span></div><button className="member-row" onClick={() => setDrawer({ type: 'account' })}><span className="avatar">A</span><span><strong>Admin</strong><small>Local owner account</small></span><span className="role-tag">Owner</span><Icon name="chevron" size={16} /></button></section>}
        {apiConnected && page === 'Settings' && <SettingsPage server={server} webservers={webservers} openSetup={() => setDrawer({ type: 'setup' })} openBackups={() => setDrawer({ type: 'backups' })} openServer={() => setDrawer({ type: 'server' })} dark={dark} setDark={setDark} />}
        <footer className="workspace-footer"><span><CloudriftsMark />Cloudrifts</span><span><i />{apiConnected ? 'API' : 'Offline'}<span className="footer-divider">/</span>{jobs[0] ? `${jobs[0].title} · ${jobs[0].status}` : 'ready'}</span></footer>
      </main>
    </div>

    {drawer && <DetailDrawer drawer={drawer} close={() => setDrawer(null)} addSite={addSite} retrySSL={retrySSLAction} copy={copy} apiConnected={apiConnected} server={server} roles={roles} webservers={webservers} switchWebserver={switchWebserverAction} refresh={refresh} notify={notify} />}
    {command && <Overlay close={() => setCommand(false)} className="command-dialog" label="Search workspace"><div className="command-input"><Icon name="search" /><input autoFocus placeholder="Jump to a page or website…" value={query} onChange={e => setQuery(e.target.value)} /><button onClick={() => setCommand(false)}><kbd>esc</kbd></button></div><div className="command-results"><span className="eyebrow">WORKSPACE</span>{nav.filter(n => n.label.toLowerCase().includes(query.toLowerCase())).map(n => <button key={n.label} onClick={() => { navigate(n.label); setCommand(false); }}><Icon name={n.icon} /><span>{n.label}</span><Icon name="arrow" size={14} /></button>)}{sites.filter(s => s.domain.includes(query.toLowerCase())).slice(0, 5).map(s => <button key={s.id} onClick={() => { setDrawer({ type: 'site', site: s }); setCommand(false); }}><Icon name="globe" /><span>{s.domain}</span><Icon name="arrow" size={14} /></button>)}</div><div className="command-footer"><span>Quick navigation</span><span><kbd>esc</kbd> to close</span></div></Overlay>}
    {toast && <div className="toast" role="status"><Icon name="check" size={16} />{toast}</div>}
  </div>;
}

function LiveStatsChart({ metric, statsHistory }: { metric: MetricName; statsHistory: ServerStats[] }) { const values = statsHistory.length ? statsHistory.map(item => metric === 'Memory' ? memoryPercent(item) : metric === 'CPU' ? Number(metricValue('CPU', item)) : 0) : [0]; const max = metric === 'Network' ? Math.max(1, ...values) : 100; const points = values.map((value, i) => [values.length === 1 ? 600 : i * 600 / (values.length - 1), 126 - Math.min(max, value) / max * 110]); const line = points.map((p, i) => `${i ? 'L' : 'M'}${p[0]},${p[1]}`).join(' '); return <div className="performance-chart live-stats-chart" aria-label={`${metric} live chart`} role="img"><div className="chart-y"><span>{metric === 'Network' ? `${max.toFixed(0)} MB` : '100%'}</span><span>{metric === 'Network' ? `${(max / 2).toFixed(0)} MB` : '50%'}</span><span>0</span></div><svg viewBox="0 0 600 135" preserveAspectRatio="none">{[16, 71, 126].map(y => <path key={y} d={`M0 ${y}h600`} stroke="currentColor" className="chart-grid" strokeDasharray="3 5" />)}<path d={`${line} L600 135 L0 135Z`} fill="rgba(123,108,240,.10)" /><path d={line} fill="none" stroke="#8a77f2" strokeWidth="2.1" strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />{points.map((point, i) => i === points.length - 1 ? <circle key={i} cx={point[0]} cy={point[1]} r="3" fill="#8a77f2" /> : null)}</svg><div className="chart-x"><span>{statsHistory[0] ? relativeTime(statsHistory[0].collected_at) : 'now'}</span><span>Live samples</span><span>{statsHistory.at(-1) ? relativeTime(statsHistory.at(-1)!.collected_at) : 'now'}</span></div></div>; }

function BackendOffline({ loading, error, onRetry }: { loading: boolean; error: string; onRetry: () => void }) {
  return <section className="route-surface backend-offline"><span className="event-icon amber"><Icon name={loading ? 'activity' : 'warning'} /></span><div><h2>{loading ? 'Connecting to Cloudrifts backend…' : 'Backend is not connected'}</h2><p>{loading ? 'Loading real panel state.' : error || 'Start cloudriftsd on port 8080 or open the installed panel URL.'}</p></div><button className="button primary" onClick={onRetry}><Icon name="activity" size={16} />Retry</button></section>;
}

function NotificationPopover({ jobs, sslIssues, close, openSite, clearJob }: { jobs: TrackedJob[]; sslIssues: Site[]; close: () => void; openSite: (site: Site) => void; clearJob: (id: string) => void }) {
  const activeJobs = jobs.filter(job => job.status === 'queued' || job.status === 'running');
  const finishedJobs = jobs.filter(job => job.status === 'done' || job.status === 'failed');
  const hasItems = activeJobs.length || finishedJobs.length || sslIssues.length;
  return <div className="notification-popover operation-center" role="dialog" aria-label="Operations and notifications">
    <div className="popover-heading"><span>Operations</span><button className="icon-button" aria-label="Close notifications" onClick={close}><Icon name="close" size={15} /></button></div>
    {!hasItems && <div className="notification-empty"><span className="event-icon mint"><Icon name="check" /></span><strong>No background work</strong><small>New jobs, SSL issues and failures will stay here.</small></div>}
    {!!activeJobs.length && <div className="notification-section"><span className="notification-label">Live tasks</span>{activeJobs.map(job => <JobNotification key={job.id} job={job} clearJob={clearJob} />)}</div>}
    {!!finishedJobs.length && <div className="notification-section"><span className="notification-label">Recent results</span>{finishedJobs.slice(0, 6).map(job => <JobNotification key={job.id} job={job} clearJob={clearJob} />)}</div>}
    {!!sslIssues.length && <div className="notification-section"><span className="notification-label">SSL attention</span>{sslIssues.slice(0, 5).map(site => <button key={site.id} className="ssl-notification" onClick={() => openSite(site)}><span className="event-icon amber"><Icon name="shield" /></span><span><strong>{site.domain}</strong><small>{site.issues?.[0]?.detail || site.sslReason || 'Certificate needs DNS or retry attention.'}</small></span><Icon name="chevron" size={15} /></button>)}</div>}
  </div>;
}

function JobNotification({ job, clearJob }: { job: TrackedJob; clearJob: (id: string) => void }) {
  const progress = jobProgress(job);
  const issue = job.issues?.[0];
  const currentStep = job.steps?.find(step => step.status === 'running') || job.steps?.find(step => step.status === 'failed') || job.steps?.find(step => step.status === 'warning') || job.steps?.filter(step => step.status === 'done').at(-1);
  const canDismiss = job.status === 'done' || job.status === 'failed';
  return <div className={`job-notification ${jobTone(job)}`}>
    <div className="job-notification-head"><span className={`event-icon ${job.status === 'failed' ? 'amber' : job.status === 'done' ? 'mint' : 'violet'}`}><Icon name={job.status === 'failed' ? 'warning' : job.status === 'done' ? 'check' : 'activity'} /></span><span><strong>{job.title}</strong><small>{job.target_name || job.type} · {jobStatusLabel(job)}</small></span>{canDismiss && <button className="icon-button" aria-label="Dismiss job notification" onClick={() => clearJob(job.id)}><Icon name="close" size={14} /></button>}</div>
    <div className="job-notification-bar"><span style={{ width: `${progress}%` }} /></div>
    <div className="job-notification-meta"><span>{currentStep?.title || 'Preparing operation'}</span><strong>{progress}%</strong></div>
    {!!job.steps?.length && <div className="job-step-strip">{job.steps.slice(0, 5).map(step => <i key={step.id} className={step.status} title={step.title} />)}</div>}
    {issue && <p className="job-notification-issue">{issue.detail || issue.title}</p>}
  </div>;
}

function AppBadge({ app }: { app: Site['app'] }) { return <span className={`app-badge ${app.toLowerCase()}`}><span>{app === 'WordPress' ? 'W' : app === 'WooCommerce' ? 'woo' : 'php'}</span>{app}</span>; }
function ActivityList({ events }: { events: UIEvent[] }) { return <div className="activity-list">{events.length ? events.map((event, i) => <div className="activity-row" key={`${event.title}-${i}`}><span className={`event-icon ${event.tone}`}><Icon name={event.icon} size={15} /></span><span><strong>{event.title}</strong><small>{event.detail}</small></span><time>{event.time}</time></div>) : <div className="empty-state">No activity yet.</div>}</div>; }
function SiteList({ sites, open }: { sites: Site[]; open: (site: Site) => void }) { return <div className="site-list">{sites.length ? sites.map(site => <button key={site.id} className="site-card" onClick={() => open(site)}><SiteThumbnail kind={site.kind} /><span><AppBadge app={site.app} /><strong>{site.domain}</strong><small>{site.documentRoot || 'Document root pending'}</small></span><span className={`site-status ${site.healthy ? 'healthy' : 'attention'}`}><i />{site.healthy ? 'Healthy' : 'Attention'}</span><Icon name="chevron" size={16} /></button>) : <div className="empty-state">No websites yet. Create the first website from New site.</div>}</div>; }
function RoleDetails({ roles }: { roles: ServerRole[] }) { const iconFor = (role: ServerRole): IconName => role.kind === 'database' ? 'database' : role.kind === 'dns' ? 'network' : role.kind === 'runtime' ? 'code' : role.kind === 'backup' ? 'backup' : 'globe'; return <div className="role-details">{roles.map(role => <div key={role.id}><span className={`event-icon ${role.status === 'running' || role.status === 'ready' ? 'mint' : 'amber'}`}><Icon name={iconFor(role)} /></span><span><strong>{role.name}</strong><small>{role.description || `Version ${role.version}`}</small></span><span className={`site-status ${role.status === 'running' || role.status === 'ready' ? 'healthy' : 'attention'}`}><i />{role.status}</span></div>)}</div>; }
function JobTrail({ jobs }: { jobs: Job[] }) { return <div className="job-trail">{jobs.length ? jobs.slice(0, 8).map(job => <div className="job-card" key={job.id}><div><strong>{job.title}</strong><small>{job.target_name || job.type}</small></div><span className={`site-status ${job.status === 'failed' ? 'attention' : 'healthy'}`}><i />{job.status}</span><div className="job-steps">{job.steps.map(step => <span key={step.id} className={step.status}>{step.title}</span>)}</div>{job.issues[0] && <p className="form-error">{job.issues[0].detail}</p>}</div>) : <div className="empty-state">No jobs yet.</div>}</div>; }
function WebserverSwitcher({ webservers, onSwitch }: { webservers: WebserverOption[]; onSwitch: (id: WebserverOption['id']) => Promise<void> }) { const [busy, setBusy] = useState(''); return <div className="webserver-grid"><h3 className="drawer-section-title">Webserver engine</h3>{webservers.map(ws => <button key={ws.id} className={`webserver-option ${ws.current ? 'active' : ''}`} disabled={!!busy || ws.current} onClick={async () => { setBusy(ws.id); try { await onSwitch(ws.id); } finally { setBusy(''); } }}><span><strong>{ws.name}</strong><small>{ws.description}</small></span><span className={`site-status ${ws.current ? 'healthy' : ws.installed ? 'attention' : 'attention'}`}><i />{busy === ws.id ? 'switching' : ws.current ? 'active' : ws.status}</span></button>)}</div>; }

function DNSPanel({ role, zones, server, refresh, notify, copy }: { role: DNSRoleState | null; zones: DNSZone[]; server: ServerSummary | null; refresh: () => Promise<PanelOverview>; notify: (message: string) => void; copy: (text: string) => void }) {
  const [busy, setBusy] = useState('');
  const [domain, setDomain] = useState('');
  const roleStatus = role?.status || server?.dns_mode || 'external';
  const setup = async () => {
    setBusy('setup');
    try {
      const result = await setupDNSRole();
      trackPanelJob(result.job);
      notify('Managed DNS setup queued');
      const finalJob = await waitJobOutside(result.job.id);
      await refresh();
      notify(finalJob?.status === 'failed' ? 'DNS setup failed · check job trail' : 'Managed DNS is ready');
    } finally {
      setBusy('');
    }
  };
  const createZone = async () => {
    const normalized = domain.trim().toLowerCase();
    if (!normalized) return;
    setBusy('zone');
    try {
      const result = await createDNSZone({ domain: normalized });
      trackPanelJob(result.job);
      setDomain('');
      notify('DNS zone apply queued');
      const finalJob = await waitJobOutside(result.job.id);
      await refresh();
      notify(finalJob?.status === 'failed' ? 'DNS zone failed · check job trail' : 'DNS zone applied');
    } catch (err) {
      notify(err instanceof Error ? err.message : 'Could not create DNS zone');
    } finally {
      setBusy('');
    }
  };
  return <div className="dns-manager"><div className="section-heading compact"><div><h3>Managed DNS</h3><span className="soft-label">{roleStatus}</span></div><button className="button secondary" disabled={!!busy} onClick={setup}><Icon name="network" size={15} />{busy === 'setup' ? 'Setting up…' : 'Setup DNS role'}</button></div><div className="dns-role-card"><span className={`event-icon ${role?.ready ? 'mint' : 'amber'}`}><Icon name="network" /></span><span><strong>{role?.mode === 'managed' ? 'PowerDNS authoritative server' : 'External DNS mode'}</strong><small>{role?.ready ? 'Ready for managed zones' : role?.mode === 'managed' ? 'Run setup to prepare PowerDNS' : 'Create zones after switching DNS mode to managed'}</small></span><button className="text-button" onClick={() => copy(role?.server_ip || server?.ipv4 || '')}>{role?.server_ip || server?.ipv4 || 'IP unavailable'}<Icon name="copy" size={14} /></button></div><div className="dns-nameservers"><code>{role?.nameserver_1 || 'ns1.your-domain.com'}</code><code>{role?.nameserver_2 || 'ns2.your-domain.com'}</code></div><div className="inline-create dns-create"><input className="text-input" placeholder="example.com" value={domain} onChange={e => setDomain(e.target.value)} /><button className="button primary" disabled={!!busy || !domain.trim()} onClick={createZone}><Icon name="plus" size={15} />Create zone</button></div>{zones.length ? zones.map(zone => <DNSZoneCard key={zone.id} zone={zone} refresh={refresh} notify={notify} />) : <div className="empty-state">No managed zones yet. Create a website or add a zone here.</div>}</div>;
}

function DNSZoneCard({ zone, refresh, notify }: { zone: DNSZone; refresh: () => Promise<PanelOverview>; notify: (message: string) => void }) {
  const [busy, setBusy] = useState('');
  const [record, setRecord] = useState({ type: 'A', name: '@', value: zone.server_ip || '', ttl: 300 });
  const apply = async () => {
    setBusy('apply');
    try {
      const result = await applyDNSZone(zone.id);
      trackPanelJob(result.job);
      notify('DNS zone apply queued');
      const finalJob = await waitJobOutside(result.job.id);
      await refresh();
      notify(finalJob?.status === 'failed' ? `${zone.domain} DNS failed · check job trail` : `${zone.domain} DNS applied`);
    } finally {
      setBusy('');
    }
  };
  const saveRecord = async () => {
    if (!record.type.trim() || !record.value.trim()) return;
    setBusy('record');
    try {
      const result = await upsertDNSRecord(zone.id, record);
      trackPanelJob(result.job);
      notify('DNS record apply queued');
      const finalJob = await waitJobOutside(result.job.id);
      await refresh();
      notify(finalJob?.status === 'failed' ? 'Record apply failed · check job trail' : 'Record applied');
    } catch (err) {
      notify(err instanceof Error ? err.message : 'Could not save DNS record');
    } finally {
      setBusy('');
    }
  };
  return <div className="dns-zone-card"><div className="dns-zone-head"><span><strong>{zone.domain}</strong><small>{zone.records.length} records · {zone.nameserver_1} / {zone.nameserver_2}</small></span><span className={`site-status ${zone.status === 'active' ? 'healthy' : 'attention'}`}><i />{zone.status}</span><button className="button secondary" disabled={!!busy} onClick={apply}>{busy === 'apply' ? 'Applying…' : 'Apply'}</button></div>{zone.issues?.[0] && <div className="inline-alert"><Icon name="warning" size={17} /><div><strong>{zone.issues[0].title}</strong><span>{zone.issues[0].detail}</span></div></div>}<div className="dns-record-grid">{zone.records.map(item => <div key={item.id} className="dns-record-row"><span>{item.type}</span><code>{item.name}</code><code>{item.value}</code><small>{item.ttl}s · {item.status}</small></div>)}</div><div className="dns-record-form"><select value={record.type} onChange={e => setRecord(prev => ({ ...prev, type: e.target.value }))}>{['A', 'AAAA', 'CNAME', 'MX', 'TXT'].map(type => <option key={type} value={type}>{type}</option>)}</select><input className="text-input" value={record.name} onChange={e => setRecord(prev => ({ ...prev, name: e.target.value }))} placeholder="@ or www" /><input className="text-input" value={record.value} onChange={e => setRecord(prev => ({ ...prev, value: e.target.value }))} placeholder="record value" /><button className="button primary" disabled={!!busy} onClick={saveRecord}>{busy === 'record' ? 'Saving…' : 'Save record'}</button></div></div>;
}

function MailPanel({ role, refresh, notify, copy }: { role: MailRoleState | null; refresh: () => Promise<PanelOverview>; notify: (message: string) => void; copy: (text: string) => void }) {
  const [busy, setBusy] = useState(false);
  const setup = async () => {
    setBusy(true);
    try {
      const result = await setupMailRole();
      trackPanelJob(result.job);
      notify('Mail role setup queued');
      const finalJob = await waitJobOutside(result.job.id);
      await refresh();
      notify(finalJob?.status === 'failed' ? 'Mail setup failed · check job trail' : 'Mail role is ready');
    } finally {
      setBusy(false);
    }
  };
  return <div className="mail-manager"><div className="section-heading compact"><div><h3>Email role</h3><span className="soft-label">{role?.status || 'disabled'}</span></div><button className="button secondary" onClick={setup} disabled={busy}><Icon name="mail" size={15} />{busy ? 'Setting up…' : 'Setup mail role'}</button></div><div className="mail-role-card"><span className={`event-icon ${role?.ready ? 'mint' : 'amber'}`}><Icon name="mail" /></span><span><strong>{role?.hostname || 'mail.your-domain.com'}</strong><small>{role?.ready ? 'Postfix and Dovecot are active' : 'Sets up SMTP, submission, IMAP and service health before mailbox features are enabled'}</small></span><button className="text-button" onClick={() => copy(role?.webmail_url || '')}>{role?.webmail_url || 'webmail not set'}<Icon name="copy" size={14} /></button></div></div>;
}

function Overlay({ children, close, className = '', label }: { children: React.ReactNode; close: () => void; className?: string; label: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => { const previous = document.activeElement as HTMLElement | null; const root = ref.current; const first = root?.querySelector<HTMLElement>('input,button,select,textarea,[tabindex="0"]'); first?.focus(); const trap = (event: KeyboardEvent) => { if (event.key !== 'Tab') return; const focusable = Array.from(root?.querySelectorAll<HTMLElement>('button:not(:disabled),input,select,textarea,a[href],[tabindex="0"]') ?? []).filter(el => el.getClientRects().length > 0); const first = focusable[0], last = focusable[focusable.length - 1]; if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); } else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); } }; root?.addEventListener('keydown', trap); const overflow = document.body.style.overflow; document.body.style.overflow = 'hidden'; return () => { root?.removeEventListener('keydown', trap); document.body.style.overflow = overflow; previous?.focus(); }; }, []);
  return <div className="overlay" onMouseDown={e => { if (e.target === e.currentTarget) close(); }}><div ref={ref} role="dialog" aria-modal="true" aria-label={label} className={className}>{children}</div></div>;
}

function DetailDrawer({ drawer, close, addSite, retrySSL, copy, apiConnected, server, roles, webservers, switchWebserver, refresh, notify }: { drawer: Exclude<Drawer, null>; close: () => void; addSite: (domain: string, app: Site['app']) => Promise<void>; retrySSL: (site: Site) => Promise<void>; copy: (text: string) => void; apiConnected: boolean; server: ServerSummary | null; roles: ServerRole[]; webservers: WebserverOption[]; switchWebserver: (id: WebserverOption['id']) => Promise<void>; refresh: () => Promise<PanelOverview>; notify: (message: string) => void }) {
  const site = drawer.type === 'site' ? drawer.site : null;
  const [siteTab, setSiteTab] = useState<'Overview' | 'Files' | 'Backups' | 'Logs'>('Overview');
  const [domain, setDomain] = useState('');
  const [app, setApp] = useState<Site['app']>('WordPress');
  const [error, setError] = useState('');
  const title = site ? site.domain : drawerTitle(drawer.type);
  return <Overlay close={close} className="detail-drawer" label={title}><div className="drawer-top"><span className="eyebrow">CLOUDRIFTS WORKSPACE</span><button className="icon-button" aria-label="Close details" onClick={close}><Icon name="close" /></button></div><div className="drawer-title"><span className="drawer-symbol"><Icon name={site ? 'globe' : drawer.type === 'create' ? 'plus' : drawer.type === 'certificates' ? 'shield' : drawer.type === 'backups' ? 'backup' : drawer.type === 'server' ? 'server' : 'settings'} size={24} /></span><h2>{title}</h2><span className="soft-label">{apiConnected ? 'Live backend' : 'Offline'}</span></div><div className="drawer-body">
    {drawer.type === 'create' && <form onSubmit={async e => { e.preventDefault(); const normalized = domain.trim().toLowerCase(); if (!/^(?!-)[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(normalized) || normalized.includes('..')) { setError('Enter a domain such as example.com'); return; } try { await addSite(normalized, app); } catch (err) { setError(err instanceof Error ? err.message : 'Website create failed'); } }}><label className="form-label" htmlFor="site-domain">Domain name</label><div className="domain-input"><Icon name="globe" /><input id="site-domain" value={domain} onChange={e => { setDomain(e.target.value); setError(''); }} placeholder="your-website.com" autoComplete="off" /></div>{error && <p className="form-error">{error}</p>}<label className="form-label">Start with</label><div className="app-options">{(['WordPress', 'WooCommerce', 'PHP'] as Site['app'][]).map(item => <button key={item} type="button" aria-pressed={app === item} className={app === item ? 'active' : ''} onClick={() => setApp(item)}><AppBadge app={item} />{app === item && <Icon name="check" size={15} />}</button>)}</div><div className="provision-summary"><div><Icon name="server" /><span>{server?.hostname || 'server'}</span><span className="status-dot" /></div><div><Icon name="code" /><span>PHP 8.3</span><small>Default</small></div><div><Icon name="shield" /><span>Automatic SSL</span><Icon name="check" size={15} /></div><div><Icon name="database" /><span>MariaDB</span><small>{app === 'PHP' ? 'Optional' : 'Included'}</small></div></div><button className="button primary full-button" type="submit"><Icon name="plus" size={16} />Create website</button></form>}
    {site && <><div className="site-drawer-preview"><SiteThumbnail kind={site.kind} /><div><AppBadge app={site.app} /><span className={`site-status ${site.healthy ? 'healthy' : 'attention'}`}><i />{site.healthy ? 'Healthy' : 'Needs attention'}</span></div></div><div className="drawer-tabs" role="group" aria-label="Website details">{(['Overview', 'Files', 'Backups', 'Logs'] as const).map(t => <button key={t} aria-pressed={siteTab === t} onClick={() => setSiteTab(t)} className={siteTab === t ? 'active' : ''}>{t}</button>)}</div>{siteTab === 'Overview' && <SiteOverview site={site} retrySSL={retrySSL} copy={copy} />}{siteTab === 'Files' && <SiteFiles site={site} notify={notify} />}{siteTab === 'Backups' && <SiteBackups site={site} notify={notify} refresh={refresh} />}{siteTab === 'Logs' && <SiteLogs site={site} />}</>}
    {drawer.type === 'server' && <><div className="drawer-server-art"><ServerIllustration /><span className="online-tag"><i />Online</span></div><dl className="detail-list"><div><dt>Hostname</dt><dd>{server?.hostname || 'unknown'}</dd></div><div><dt>Operating system</dt><dd>{server?.os || 'unknown'}</dd></div><div><dt>IP address</dt><dd>{server?.ipv4 || 'unknown'} <button className="inline-icon" aria-label="Copy server IP" onClick={() => copy(server?.ipv4 || '')}><Icon name="copy" size={13} /></button></dd></div><div><dt>Backup path</dt><dd>{server?.backup_path || '/backups'}</dd></div></dl><RoleDetails roles={roles} /><WebserverSwitcher webservers={webservers} onSwitch={switchWebserver} /></>}
    {drawer.type === 'certificates' && <CertificateDrawer refresh={refresh} notify={notify} copy={copy} />}
    {drawer.type === 'backups' && <GlobalBackups refresh={refresh} notify={notify} />}
    {drawer.type === 'setup' && <SetupDrawer server={server} refresh={refresh} notify={notify} />}
    {drawer.type === 'account' && <><div className="account-summary"><span className="avatar large">A</span><h3>Admin</h3><span>Workspace owner</span></div><dl className="detail-list"><div><dt>Workspace</dt><dd>My workspace</dd></div><div><dt>Environment</dt><dd>{apiConnected ? 'Live backend' : 'Offline'}</dd></div><div><dt>Version</dt><dd>Cloudrifts 0.1</dd></div><div><dt>Server mode</dt><dd>Single server</dd></div></dl></>}
    {drawer.type === 'help' && <div className="help-content"><div><span className="event-icon violet"><Icon name="globe" /></span><h3>Real actions</h3><p>Create sites, retry SSL, browse files, create/restore backups and switch webservers through the backend job engine.</p></div><div><span className="event-icon mint"><Icon name="activity" /></span><h3>Job trail</h3><p>Every action records steps, logs and recovery details so users do not hunt through raw logs first.</p></div></div>}
  </div><div className="drawer-bottom"><span><i />{apiConnected ? 'Live backend' : 'Offline'}</span><button className="text-button" onClick={close}>Close<kbd>esc</kbd></button></div></Overlay>;
}

function SiteOverview({ site, retrySSL, copy }: { site: Site; retrySSL: (site: Site) => Promise<void>; copy: (text: string) => void }) { return <><dl className="detail-list"><div><dt>Server</dt><dd>Master server</dd></div><div><dt>PHP version</dt><dd>{site.phpVersion || '8.3'}</dd></div><div><dt>Storage</dt><dd>{site.storage}</dd></div><div><dt>Certificate</dt><dd>{site.sslStatus === 'active' ? "Let's Encrypt" : site.sslStatus || 'pending'}</dd></div><div><dt>Document root</dt><dd><code>{site.documentRoot}</code></dd></div></dl>{!!site.issues?.length && <div className="ssl-action-panel"><div className="inline-alert"><Icon name="warning" size={19} /><div><strong>{site.issues[0].title}</strong><span>{site.issues[0].detail}</span></div></div>{site.requiredRecords?.[0] && <div className="dns-record"><span>{site.requiredRecords[0].type}</span><code>{site.requiredRecords[0].name}</code><code>{site.requiredRecords[0].value}</code><button className="icon-button" aria-label="Copy required DNS record" onClick={() => copy(`${site.requiredRecords?.[0].type} ${site.requiredRecords?.[0].name} ${site.requiredRecords?.[0].value}`)}><Icon name="copy" size={15} /></button></div>}<p className="form-note compact-note"><Icon name="shield" size={16} /><span>{site.issues[0].fix}</span></p><button className="button primary full-button" onClick={() => retrySSL(site)}><Icon name="shield" size={16} />Retry SSL</button></div>}<button className="button secondary full-button" onClick={() => copy(site.domain)}><Icon name="copy" size={16} />Copy domain</button></>; }

function SiteFiles({ site, notify }: { site: Site; notify: (message: string) => void }) { const [path, setPath] = useState('/'); const [entries, setEntries] = useState<FileEntry[]>([]); const [selected, setSelected] = useState(''); const [content, setContent] = useState(''); const [newName, setNewName] = useState(''); const [loading, setLoading] = useState(false); const load = useCallback(async (next: string) => { setLoading(true); try { const result = await listWebsiteFiles(site.id, next); setPath(result.path || '/'); setEntries(result.entries); } finally { setLoading(false); } }, [site.id]); useEffect(() => { load('/').catch(err => notify(err instanceof Error ? err.message : 'Could not load files')); }, [load, notify]); const openEntry = async (entry: FileEntry) => { if (entry.type === 'directory') { await load(entry.path); return; } const file = await readWebsiteFile(site.id, entry.path); setSelected(file.path); setContent(file.content); }; const save = async () => { if (!selected) return; await writeWebsiteFile(site.id, selected, content); notify('File saved'); await load(path); }; const create = async () => { const name = newName.trim(); if (!name) return; const target = path ? `${path}/${name}` : name; await writeWebsiteFile(site.id, target, ''); setNewName(''); notify('File created'); await load(path); }; return <div className="real-panel"><div className="file-path"><Icon name="folder" size={16} /><code>/{path}</code>{path && <button className="text-button" onClick={() => load(path.split('/').slice(0, -1).join('/'))}>Up</button>}</div><div className="inline-create"><input className="text-input" placeholder="new-file.php" value={newName} onChange={e => setNewName(e.target.value)} /><button className="button secondary" onClick={create}><Icon name="plus" size={15} />New file</button></div>{loading ? <div className="empty-state">Loading files…</div> : entries.map(entry => <button className="file-row" key={entry.path} onClick={() => openEntry(entry)}><Icon name={entry.type === 'directory' ? 'folder' : 'file'} size={17} /><span>{entry.name}</span><small>{entry.mode} · {formatBytes(entry.size)}</small><Icon name="chevron" size={14} /></button>)}{selected && <div className="file-editor"><strong>{selected}</strong><textarea value={content} onChange={e => setContent(e.target.value)} rows={12} /><button className="button primary full-button" onClick={save}>Save file</button></div>}</div>; }

function SiteBackups({ site, notify, refresh }: { site: Site; notify: (message: string) => void; refresh: () => Promise<PanelOverview> }) { const [backups, setBackups] = useState<Backup[]>([]); const [busy, setBusy] = useState(''); const load = useCallback(async () => { setBackups(await getWebsiteBackups(site.id)); }, [site.id]); useEffect(() => { load().catch(err => notify(err instanceof Error ? err.message : 'Could not load backups')); }, [load, notify]); const create = async () => { setBusy('create'); try { const result = await createWebsiteBackup(site.id); trackPanelJob(result.job); notify('Backup job queued'); await waitJobOutside(result.job.id); await load(); await refresh(); notify('Backup finished'); } finally { setBusy(''); } }; const restore = async (backup: Backup) => { setBusy(backup.id); try { const result = await restoreWebsiteBackup(site.id, backup.id); trackPanelJob(result.job); notify('Restore job queued'); await waitJobOutside(result.job.id); await load(); await refresh(); notify('Restore finished'); } finally { setBusy(''); } }; return <div className="snapshot-list"><button className="button primary full-button" onClick={create} disabled={!!busy}><Icon name="backup" size={16} />Create backup</button>{backups.length ? backups.map(backup => <div className="backup-row" key={backup.id}><span className="event-icon mint"><Icon name="backup" /></span><span><strong>{new Date(backup.created_at).toLocaleString()}</strong><small>{backup.status} · {formatBytes(backup.size_bytes)} · {backup.path}</small></span><button className="button secondary" disabled={!!busy || backup.status !== 'completed'} onClick={() => restore(backup)}>{busy === backup.id ? 'Restoring…' : 'Restore'}</button></div>) : <div className="empty-state">No backups yet. Create one now.</div>}</div>; }

function SiteLogs({ site }: { site: Site }) { const [kind, setKind] = useState('error'); const [lines, setLines] = useState<string[]>([]); useEffect(() => { getWebsiteLogs(site.id, kind, 200).then(result => setLines(result.lines)).catch(err => setLines([err instanceof Error ? err.message : 'Could not load logs'])); }, [site.id, kind]); return <div className="logs-panel"><div className="drawer-tabs">{['error', 'access', 'php'].map(item => <button key={item} className={kind === item ? 'active' : ''} onClick={() => setKind(item)}>{item}</button>)}</div><pre>{lines.length ? lines.join('\n') : 'No log lines yet.'}</pre></div>; }

function CertificateDrawer({ refresh, notify, copy }: { refresh: () => Promise<PanelOverview>; notify: (message: string) => void; copy: (text: string) => void }) { const [sites, setSites] = useState<Site[]>([]); useEffect(() => { refresh().then(overview => setSites(overview.websites.map(siteFromAPI))).catch(err => notify(err instanceof Error ? err.message : 'Could not load certificates')); }, [refresh, notify]); return <div className="certificate-list">{sites.map(site => <div key={site.id}><Icon name={site.healthy ? 'lock' : 'warning'} size={17} /><span><strong>{site.domain}</strong><small>{site.healthy ? "Let's Encrypt · active" : site.issues?.[0]?.detail || 'Needs attention'}</small></span><span className={`site-status ${site.healthy ? 'healthy' : 'attention'}`}>{site.healthy ? 'Active' : 'Check'}</span>{site.requiredRecords?.[0] && <button className="icon-button" onClick={() => copy(`${site.requiredRecords?.[0].type} ${site.requiredRecords?.[0].name} ${site.requiredRecords?.[0].value}`)}><Icon name="copy" size={15} /></button>}</div>)}</div>; }
function GlobalBackups({ refresh, notify }: { refresh: () => Promise<PanelOverview>; notify: (message: string) => void }) { const [sites, setSites] = useState<Site[]>([]); useEffect(() => { refresh().then(overview => setSites(overview.websites.map(siteFromAPI))).catch(err => notify(err instanceof Error ? err.message : 'Could not load websites')); }, [refresh, notify]); return <div className="backup-total"><Icon name="backup" size={28} /><span><strong>{sites.length} websites available</strong><small>Open a website → Backups to create or restore real snapshots.</small></span></div>; }
function SetupDrawer({ server, refresh, notify, onSetupApplied }: { server: ServerSummary | null; refresh: () => Promise<PanelOverview>; notify: (message: string) => void; onSetupApplied?: () => Promise<void> }) {
  const [settings, setSettings] = useState<SetupSettings | null>(null);
  const [savedSettings, setSavedSettings] = useState<SetupSettings | null>(null);
  const [delegation, setDelegation] = useState<DelegationRecord[]>([]);
  const [dnsCheck, setDNSCheck] = useState<SetupDNSCheck | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  const [busyStep, setBusyStep] = useState<number | null>(null);
  const [checkingDNS, setCheckingDNS] = useState(false);
  const [step, setStep] = useState(0);
  const [maxStep, setMaxStep] = useState(0);
  const [error, setError] = useState('');
  const serverIP = server?.ipv4 || dnsCheck?.server_ip || '';

  useEffect(() => { getSetupSettings().then(result => { setSettings(result); setSavedSettings(result); const stored = Number(localStorage.getItem(setupMaxStepKey) || '0'); const backendStep = result.setup_completed ? 3 : result.panel_url && result.webserver && result.database ? 2 : result.panel_url ? 1 : 0; setMaxStep(Math.max(stored, backendStep)); }).catch(err => setError(err instanceof Error ? err.message : 'Could not load setup')); }, []);
  useEffect(() => {
    if (!settings?.panel_url) return;
    const handle = window.setTimeout(() => {
      getSuggestions(settings.panel_url).then(suggested => setSettings(current => current ? ({ ...current, webmail_url: suggested.webmail_url || current.webmail_url || '', phpmyadmin_url: suggested.phpmyadmin_url || current.phpmyadmin_url || '', nameserver_1: current.nameserver_1 || suggested.nameserver_1 || '', nameserver_2: current.nameserver_2 || suggested.nameserver_2 || '' }) : current)).catch(() => undefined);
    }, 220);
    return () => window.clearTimeout(handle);
  }, [settings?.panel_url]);

  useEffect(() => {
    if (!job || job.status === 'done' || job.status === 'failed') return;
    const handle = window.setInterval(() => {
      getJob(job.id).then(setJob).catch(() => undefined);
    }, 900);
    return () => window.clearInterval(handle);
  }, [job?.id, job?.status]);

  const update = <K extends keyof SetupSettings,>(key: K, value: SetupSettings[K]) => setSettings(current => {
    if (!current) return current;
    setError('');
    if (key !== 'panel_url') return { ...current, [key]: value };
    const nextPanel = normalizeSetupHost(String(value));
    const nextDerived = setupDerivedURLs(nextPanel);
    return { ...current, panel_url: String(value), webmail_url: nextDerived.webmail_url, phpmyadmin_url: nextDerived.phpmyadmin_url, nameserver_1: isDerivedSetupValue(current.nameserver_1, 'nameserver_1', current.panel_url) ? nextDerived.nameserver_1 : current.nameserver_1, nameserver_2: isDerivedSetupValue(current.nameserver_2, 'nameserver_2', current.panel_url) ? nextDerived.nameserver_2 : current.nameserver_2 };
  });

  const runDNSCheck = useCallback(async () => {
    if (!settings?.panel_url) return null;
    setCheckingDNS(true);
    try {
      const result = await checkSetupDNS(settings.panel_url, settings.dns_mode || 'external');
      setDNSCheck(result);
      return result;
    } catch (err) {
      notify(err instanceof Error ? err.message : 'DNS check failed');
      return null;
    } finally {
      setCheckingDNS(false);
    }
  }, [settings?.panel_url, settings?.dns_mode, notify]);

  useEffect(() => {
    if (!settings?.panel_url) return;
    const handle = window.setTimeout(() => { runDNSCheck().catch(() => undefined); }, 500);
    return () => window.clearTimeout(handle);
  }, [settings?.panel_url, settings?.dns_mode, runDNSCheck]);

  const buildPayload = (source: SetupSettings): SetupSettings => {
    const normalizedPanel = normalizeSetupHost(source.panel_url);
    const derived = setupDerivedURLs(normalizedPanel);
    return { ...source, panel_url: normalizedPanel, webmail_url: derived.webmail_url, phpmyadmin_url: derived.phpmyadmin_url, nameserver_1: source.nameserver_1 || derived.nameserver_1, nameserver_2: source.nameserver_2 || derived.nameserver_2, dns_mode: source.dns_mode || 'managed', email_enabled: false, backup_path: source.backup_path || '/backups' };
  };

  const saveCurrentStep = async (targetStep = step) => {
    if (!settings) return null;
    const payload = buildPayload(settings);
    if (!/^(?!-)[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(payload.panel_url)) { setError('Enter a panel hostname like panel.example.com'); setStep(0); return null; }
    setBusyStep(targetStep); setError('');
    try {
      const saved = await saveSetupSettings(payload);
      setSettings(saved); setSavedSettings(saved);
      if (targetStep <= 1) await runDNSCheck();
      setMaxStep(current => { const next = Math.max(current, Math.min(3, targetStep + 1)); localStorage.setItem(setupMaxStepKey, String(next)); return next; });
      notify(targetStep === 0 ? 'Panel identity saved' : targetStep === 1 ? 'DNS route saved' : targetStep === 2 ? 'Server stack saved' : 'Setup saved');
      return saved;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save setup');
      return null;
    } finally { setBusyStep(null); }
  };

  const continueStep = async () => {
    const saved = await saveCurrentStep(step);
    if (!saved) return;
    setStep(Math.min(3, step + 1));
  };

  const apply = async () => {
    if (!settings) return;
    const payload = buildPayload(settings);
    if (!/^(?!-)[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(payload.panel_url)) { setError('Enter a panel hostname like panel.example.com'); setStep(0); return; }
    setBusyStep(3); setError(''); setStep(3); setMaxStep(3); localStorage.setItem(setupMaxStepKey, '3');
    try {
      const result = await applySetup(payload);
      trackPanelJob(result.job);
      setSettings(result.settings); setSavedSettings(result.settings); setDelegation(result.delegation_records); setJob(result.job); notify('Setup is running');
      let finalJob: Job | null = null;
      for (let i = 0; i < 70; i++) {
        await delay(i < 10 ? 700 : 1300);
        const latestJob = await getJob(result.job.id).catch(() => null);
        if (latestJob) { setJob(latestJob); if (latestJob.status === 'done' || latestJob.status === 'failed') { finalJob = latestJob; break; } }
      }
      await refresh();
      const latest = await getSetupSettings();
      setSettings(latest); setSavedSettings(latest);
      if (finalJob?.status === 'failed') {
        setError(finalJob.issues[0]?.detail || 'Setup failed. Open operation center for the exact step.');
        notify('Setup failed · reason is visible here');
      } else {
        notify(latest.panel_ssl_status === 'active' ? 'Setup completed with trusted SSL' : 'Setup applied · DNS/SSL can be rechecked');
        await onSetupApplied?.();
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Setup apply failed');
    } finally { setBusyStep(null); }
  };

  if (!settings) return <div className="empty-state">Loading setup…</div>;

  const normalizedPanel = normalizeSetupHost(settings.panel_url);
  const rootDomain = setupRootDomain(settings.panel_url);
  const derived = setupDerivedURLs(settings.panel_url);
  const changed = JSON.stringify(buildPayload(settings)) !== JSON.stringify(savedSettings ? buildPayload(savedSettings) : null);
  const managedRecords = delegation.length ? delegation : [
    { type: 'GLUE', name: settings.nameserver_1 || derived.nameserver_1, value: serverIP, where: 'Registrar child nameserver', status: dnsCheck?.nameserver_a_ok ? 'verified' : 'required', detail: 'Create child nameserver host record.' },
    { type: 'GLUE', name: settings.nameserver_2 || derived.nameserver_2, value: serverIP, where: 'Registrar child nameserver', status: dnsCheck?.nameserver_a_ok ? 'verified' : 'required', detail: 'Phase 1 can point ns2 to same IP.' },
    { type: 'NS', name: rootDomain, value: settings.nameserver_1 || derived.nameserver_1, where: 'Registrar nameserver', status: dnsCheck?.delegation_ok ? 'verified' : 'required', detail: 'Set domain nameserver.' },
    { type: 'NS', name: rootDomain, value: settings.nameserver_2 || derived.nameserver_2, where: 'Registrar nameserver', status: dnsCheck?.delegation_ok ? 'verified' : 'required', detail: 'Set domain nameserver.' },
  ];
  const manualRecords = dnsCheck?.required_records?.length ? dnsCheck.required_records : [{ type: 'A', name: normalizedPanel || `panel.${rootDomain}`, value: serverIP, where: 'Your DNS provider', status: 'required', detail: 'Required for panel SSL.' }];
  const stepModels = [
    { label: 'Panel', icon: 'globe' as IconName, done: !!savedSettings?.panel_url, subtitle: savedSettings?.panel_url || 'URL' },
    { label: 'DNS', icon: 'network' as IconName, done: maxStep > 1 || dnsCheck?.status === 'verified', subtitle: settings.dns_mode === 'managed' ? 'ns1/ns2' : 'manual' },
    { label: 'Stack', icon: 'server' as IconName, done: maxStep > 2 || (!!savedSettings?.webserver && !!savedSettings?.database), subtitle: webserverLabel(settings.webserver) },
    { label: 'Launch', icon: 'shield' as IconName, done: settings.setup_completed || job?.status === 'done', subtitle: job?.status || 'apply' },
  ];
  const dnsTone = dnsCheck?.status === 'verified' ? 'healthy' : dnsCheck?.status === 'partial' ? 'attention' : 'attention';
  const setupProgress = job ? Math.round((job.steps.filter(item => item.status === 'done' || item.status === 'warning').length / Math.max(1, job.steps.length)) * 100) : settings.setup_completed ? 100 : Math.round((maxStep / 3) * 72);

  return <div className="setup-wizard pro-setup setup-product-flow">
    <div className="setup-rail">{stepModels.map((item, index) => {
      const locked = index > maxStep;
      return <button key={item.label} className={`${step === index ? 'active' : ''} ${item.done ? 'done' : ''} ${locked ? 'locked' : ''}`} disabled={locked || busyStep !== null} onClick={() => index <= maxStep && setStep(index)}>
        <span><Icon name={item.icon} size={15} /></span><strong>{item.label}</strong><small>{locked ? 'locked' : item.subtitle}</small>{item.done && <i><Icon name="check" size={12} /></i>}
      </button>;
    })}</div>

    <div className="setup-workbench">
      <div className="setup-canvas">
        {step === 0 && <section className="setup-stage-card panel-stage refined"><div className="stage-art"><div className="domain-orb"><CloudriftsMark /><i /><i /></div><span className={`site-status ${settings.panel_ssl_status === 'active' ? 'healthy' : 'attention'}`}><i />SSL</span></div><div className="stage-copy"><span className="brand-chip"><Icon name="shield" size={14} /> panel identity</span><h2>Panel URL</h2><div className="domain-input jumbo"><Icon name="globe" /><input value={settings.panel_url} onChange={e => update('panel_url', e.target.value as SetupSettings['panel_url'])} placeholder="panel.example.com" autoComplete="off" /></div><div className="setup-derived-strip"><span><Icon name="mail" size={14} />{derived.webmail_url}</span><span><Icon name="database" size={14} />{derived.phpmyadmin_url}</span><button className="info-dot" title="Reserved automatically. Edit later from Settings."><Icon name="help" size={13} /></button></div>{dnsCheck && <DNSCheckPill check={dnsCheck} />}</div></section>}

        {step === 1 && <section className="setup-stage-card"><div className="stage-heading"><div><span className="brand-chip"><Icon name="network" size={14} /> dns route</span><h2>DNS mode</h2></div><button className="button secondary" onClick={() => runDNSCheck()} disabled={checkingDNS || !settings.panel_url}><Icon name="activity" size={15} />{checkingDNS ? 'Checking…' : 'Recheck'}</button></div><div className="dns-choice-grid"><button className={`dns-choice ${settings.dns_mode === 'managed' ? 'active' : ''}`} onClick={() => update('dns_mode', 'managed')}><span className="choice-art managed"><Icon name="server" /></span><strong>Cloudrifts DNS</strong><small>ns1/ns2 on this server</small><Icon name={settings.dns_mode === 'managed' ? 'check' : 'chevron'} /></button><button className={`dns-choice ${settings.dns_mode === 'external' ? 'active' : ''}`} onClick={() => update('dns_mode', 'external')}><span className="choice-art external"><Icon name="globe" /></span><strong>Manual DNS</strong><small>Use your provider</small><Icon name={settings.dns_mode === 'external' ? 'check' : 'chevron'} /></button></div><div className={`dns-verdict ${dnsTone}`}><span className="event-icon"><Icon name={dnsCheck?.status === 'verified' ? 'check' : 'warning'} /></span><div><strong>{dnsCheck?.title || 'Ready to verify'}</strong><small>{dnsCheck?.detail || 'Save this step, then recheck delegation whenever records propagate.'}</small></div><span>{serverIP || 'IP detecting'}</span></div>{settings.dns_mode === 'managed' ? <ManagedDNSSetup records={managedRecords} serverIP={serverIP} /> : <ManualDNSSetup records={manualRecords} />}</section>}

        {step === 2 && <section className="setup-stage-card"><div className="stage-heading"><div><span className="brand-chip"><Icon name="server" size={14} /> server stack</span><h2>Defaults</h2></div><button className="info-dot" title="Mail role installs later from Server when needed."><Icon name="help" size={14} /></button></div><div className="stack-option-grid"><OptionCard active={settings.webserver === 'nginx'} icon="globe" title="Nginx" detail="fast reloads" onClick={() => update('webserver', 'nginx')} /><OptionCard active={settings.webserver === 'apache'} icon="server" title="Apache" detail="PHP-FPM proxy" onClick={() => update('webserver', 'apache')} /><OptionCard active={settings.database === 'mariadb'} icon="database" title="MariaDB" detail="recommended" onClick={() => update('database', 'mariadb')} /><OptionCard active={settings.database === 'mysql'} icon="database" title="MySQL" detail="compatible" onClick={() => update('database', 'mysql')} /></div><div className="backup-inline"><span><Icon name="backup" />Backup vault</span><div className="domain-input"><input value={settings.backup_path} onChange={e => update('backup_path', e.target.value)} placeholder="/backups" /></div></div></section>}

        {step === 3 && <section className="setup-stage-card launch-stage refined-launch"><div className="launch-orbit"><CloudriftsMark /><span /><span /></div><span className="brand-chip"><Icon name="settings" size={14} /> launch</span><h2>{job ? job.status === 'done' ? 'Setup complete' : job.status === 'failed' ? 'Needs attention' : 'Applying setup' : 'Ready'}</h2><div className="launch-summary"><span><Icon name="globe" />{normalizedPanel || 'panel URL missing'}</span><span><Icon name="network" />{settings.dns_mode === 'managed' ? 'Cloudrifts DNS' : 'Manual DNS'}</span><span><Icon name="server" />{webserverLabel(settings.webserver)}</span><span><Icon name="database" />{settings.database === 'mariadb' ? 'MariaDB' : settings.database}</span></div>{job && <SetupLiveJob job={job} progress={setupProgress} />}{settings.panel_ssl_reason && <div className="inline-alert"><Icon name={settings.panel_ssl_status === 'active' ? 'shield' : 'warning'} size={19} /><div><strong>Panel SSL: {settings.panel_ssl_status}</strong><span>{settings.panel_ssl_reason}</span></div></div>}</section>}
      </div>
      <aside className="setup-sidecar"><div className="sidecar-orb"><CloudriftsMark /></div><strong>{stepModels[step].label}</strong><span>{busyStep !== null ? 'Saving…' : changed ? 'Unsaved changes' : job?.status === 'running' ? 'Running' : normalizedPanel ? 'Saved' : 'Waiting'}</span><div className="sidecar-meter"><i style={{ width: `${Math.max(8, setupProgress)}%` }} /></div><div className="sidecar-stack"><span><Icon name="globe" />{normalizedPanel || 'Panel URL'}</span><span><Icon name="network" />{settings.dns_mode === 'managed' ? 'Managed DNS' : 'Manual DNS'}</span><span><Icon name="shield" />{settings.panel_ssl_status || 'pending'}</span></div></aside>
    </div>

    {error && <div className="inline-alert setup-error"><Icon name="warning" size={18} /><div><strong>Action needed</strong><span>{error}</span></div></div>}
    <div className="setup-actions"><button className="button secondary" disabled={step === 0 || busyStep !== null} onClick={() => setStep(Math.max(0, step - 1))}><Icon name="chevron" size={15} />Back</button>{step < 3 ? <button className="button primary" disabled={busyStep !== null || !normalizedPanel} onClick={continueStep}>{busyStep === step ? 'Saving…' : changed ? 'Save & continue' : 'Continue'}<Icon name="arrow" size={15} /></button> : job?.status === 'done' || settings.setup_completed ? <button className="button primary" onClick={() => onSetupApplied?.()}><Icon name="dashboard" size={16} />Enter dashboard</button> : <button className="button primary" onClick={apply} disabled={busyStep !== null || !normalizedPanel}><Icon name="settings" size={16} />{busyStep === 3 ? 'Launching…' : 'Launch setup'}</button>}</div>
  </div>;
}


function SetupLiveJob({ job, progress }: { job: Job; progress: number }) {
  const tone = job.status === 'failed' ? 'attention' : job.status === 'done' ? 'healthy' : 'running';
  return <div className={`setup-live-job ${tone}`}><div className="live-job-top"><span className="event-icon"><Icon name={job.status === 'failed' ? 'warning' : job.status === 'done' ? 'check' : 'activity'} /></span><div><strong>{job.title}</strong><small>{job.status}</small></div><b>{progress}%</b></div><div className="live-progress"><i style={{ width: `${Math.max(8, progress)}%` }} /></div><div className="live-step-grid">{job.steps.map(item => <span key={item.id} className={item.status}><Icon name={item.status === 'done' ? 'check' : item.status === 'failed' ? 'warning' : 'activity'} size={13} />{item.title}</span>)}</div>{job.issues[0] && <p className="form-error">{job.issues[0].detail}</p>}</div>;
}

function DNSCheckPill({ check }: { check: SetupDNSCheck }) { return <div className={`dns-mini-status ${check.status}`}><Icon name={check.status === 'verified' ? 'check' : check.status === 'partial' ? 'activity' : 'warning'} size={15} /><span>{check.title}</span><small>{check.server_ip}</small></div>; }
function OptionCard({ active, icon, title, detail, onClick }: { active: boolean; icon: IconName; title: string; detail: string; onClick: () => void }) { return <button className={`stack-option ${active ? 'active' : ''}`} onClick={onClick}><span><Icon name={icon} /></span><strong>{title}</strong><small>{detail}</small>{active && <Icon name="check" size={15} />}</button>; }
function ManagedDNSSetup({ records, serverIP }: { records: DelegationRecord[]; serverIP: string }) { return <div className="managed-dns-card"><div className="nameserver-hero"><div><span>ns1</span><strong>{records[0]?.name}</strong><code>{serverIP}</code></div><div><span>ns2</span><strong>{records[1]?.name}</strong><code>{serverIP}</code></div></div><div className="compact-records">{records.slice(2, 4).map((record, index) => <SetupRecord key={`${record.type}-${index}`} record={record} />)}</div></div>; }
function ManualDNSSetup({ records }: { records: DelegationRecord[] }) { return <div className="manual-record-card"><div className="record-card-head"><strong>Records to add</strong><button className="info-dot" title="Add these where your DNS is hosted. Mail can be installed later from Server."><Icon name="help" size={13} /></button></div>{records.map((record, index) => <SetupRecord key={`${record.type}-${record.name}-${index}`} record={record} />)}</div>; }
function SetupRecord({ record }: { record: DelegationRecord }) { return <div className={`setup-record ${record.status}`}><span>{record.type}</span><code>{record.name}</code><code>{record.value}</code><button className="icon-button" title={record.detail} onClick={() => navigator.clipboard?.writeText(`${record.type} ${record.name} ${record.value}`).catch(() => undefined)}><Icon name="copy" size={14} /></button></div>; }
function SettingsPage({ server, webservers, openSetup, openBackups, openServer, dark, setDark }: { server: ServerSummary | null; webservers: WebserverOption[]; openSetup: () => void; openBackups: () => void; openServer: () => void; dark: boolean; setDark: (value: boolean) => void }) { return <section className="route-surface settings-surface"><div className="section-heading"><h2>Workspace settings</h2><span className="soft-label">Live backend</span></div><div className="setting-row"><span><strong>Appearance</strong><small>Your preferred workspace theme</small></span><div className="theme-options"><button className={!dark ? 'active' : ''} onClick={() => setDark(false)}><Icon name="sun" />Light</button><button className={dark ? 'active' : ''} onClick={() => setDark(true)}><Icon name="moon" />Dark</button></div></div>{[{ name: 'Service URLs', detail: server?.hostname || 'Panel hostname', action: openSetup }, { name: 'Backup storage', detail: server?.backup_path || '/backups', action: openBackups }, { name: 'Server configuration', detail: webserverLabel(webservers.find(w => w.current)?.id), action: openServer }].map(item => <button className="setting-row setting-button" key={item.name} onClick={item.action}><span><strong>{item.name}</strong><small>{item.detail}</small></span><Icon name="chevron" /></button>)}</section>; }
function drawerTitle(type: Exclude<Drawer, null>['type']) { return type === 'create' ? 'Create a website' : type === 'setup' ? 'Setup' : type === 'certificates' ? 'SSL certificates' : type === 'backups' ? 'Backups' : type === 'server' ? 'Master server' : type === 'help' ? 'Runbook' : 'Your workspace'; }
function metricValue(metric: MetricName, stats: ServerStats | null) { if (metric === 'Memory') return String(memoryPercent(stats)); if (metric === 'Network') return '0'; const load = Number((stats?.load_avg || '').split(' ')[0] || '0'); const cpus = stats?.cpu_count || 1; return String(Math.min(100, Math.round((load / cpus) * 100))); }
function memoryPercent(stats: ServerStats | null) { const total = stats?.memory_kb?.MemTotal || 0; const available = stats?.memory_kb?.MemAvailable || 0; if (!total) return 0; return Math.max(0, Math.min(100, Math.round(((total - available) / total) * 100))); }
function memoryLabel(stats: ServerStats | null) { const total = stats?.memory_kb?.MemTotal || 0; const available = stats?.memory_kb?.MemAvailable || 0; if (!total) return 'memory unavailable'; return `${formatBytes((total - available) * 1024)} / ${formatBytes(total * 1024)}`; }
async function waitJobOutside(jobId: string) { for (let i = 0; i < 40; i++) { await delay(i < 6 ? 800 : 1600); const job = await getJob(jobId).catch(() => null); if (job && (job.status === 'done' || job.status === 'failed')) return job; } return null; }

createRoot(document.getElementById('root')!).render(<App />);
