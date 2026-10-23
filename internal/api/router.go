package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudrifts/panel/internal/config"
	"github.com/cloudrifts/panel/internal/panel"
	"github.com/cloudrifts/panel/internal/settings"
	"github.com/cloudrifts/panel/internal/setupauth"
	"github.com/cloudrifts/panel/internal/systemstats"
)

type Router struct {
	cfg      config.Config
	logger   *slog.Logger
	settings *settings.Store
	panel    *panel.Service
	mux      *http.ServeMux
}

func NewRouter(cfg config.Config, logger *slog.Logger) http.Handler {
	settingsStore := settings.NewStore(cfg.DataDir)
	r := &Router{
		cfg:      cfg,
		logger:   logger,
		settings: &settingsStore,
		panel:    panel.NewService(cfg, &settingsStore),
		mux:      http.NewServeMux(),
	}
	r.routes()
	return withCORS(r.mux)
}

func (r *Router) routes() {
	r.mux.HandleFunc("GET /api/health", r.health)

	r.mux.HandleFunc("GET /api/setup/state", r.setupState)
	r.mux.HandleFunc("GET /api/setup/settings", r.getSetupSettings)
	r.mux.HandleFunc("PUT /api/setup/settings", r.putSetupSettings)
	r.mux.HandleFunc("POST /api/setup/apply", r.applySetup)
	r.mux.HandleFunc("GET /api/setup/suggestions", r.setupSuggestions)
	r.mux.HandleFunc("GET /api/setup/dns-check", r.setupDNSCheck)
	r.mux.HandleFunc("GET /api/setup/sso/{token}", r.validateSetupSSO)

	r.mux.HandleFunc("GET /api/panel/overview", r.panelOverview)
	r.mux.HandleFunc("GET /api/websites", r.websites)
	r.mux.HandleFunc("POST /api/websites", r.createWebsite)
	r.mux.HandleFunc("GET /api/websites/{id}", r.website)
	r.mux.HandleFunc("GET /api/websites/{id}/diagnostics", r.websiteDiagnostics)
	r.mux.HandleFunc("POST /api/websites/{id}/ssl/retry", r.retryWebsiteSSL)
	r.mux.HandleFunc("GET /api/websites/{id}/backups", r.websiteBackups)
	r.mux.HandleFunc("POST /api/websites/{id}/backups", r.createWebsiteBackup)
	r.mux.HandleFunc("POST /api/websites/{id}/backups/{backup_id}/restore", r.restoreWebsiteBackup)
	r.mux.HandleFunc("GET /api/websites/{id}/files", r.websiteFiles)
	r.mux.HandleFunc("GET /api/websites/{id}/files/read", r.readWebsiteFile)
	r.mux.HandleFunc("PUT /api/websites/{id}/files/write", r.writeWebsiteFile)
	r.mux.HandleFunc("GET /api/websites/{id}/logs", r.websiteLogs)
	r.mux.HandleFunc("GET /api/jobs", r.jobs)
	r.mux.HandleFunc("GET /api/jobs/{id}", r.job)
	r.mux.HandleFunc("GET /api/activity", r.activity)
	r.mux.HandleFunc("GET /api/server/roles", r.roles)
	r.mux.HandleFunc("GET /api/server/webservers", r.webservers)
	r.mux.HandleFunc("POST /api/server/webservers/switch", r.switchWebserver)
	r.mux.HandleFunc("GET /api/server/stats", r.serverStats)
	r.mux.HandleFunc("GET /api/dns/zones", r.dnsZones)
	r.mux.HandleFunc("POST /api/dns/setup", r.setupDNSRole)
	r.mux.HandleFunc("POST /api/dns/zones", r.createDNSZone)
	r.mux.HandleFunc("POST /api/dns/zones/{id}/apply", r.applyDNSZone)
	r.mux.HandleFunc("POST /api/dns/zones/{id}/records", r.upsertDNSRecord)
	r.mux.HandleFunc("GET /api/mail/role", r.mailRole)
	r.mux.HandleFunc("POST /api/mail/setup", r.setupMailRole)
	r.mux.HandleFunc("GET /", r.frontend)
}

func (r *Router) health(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "cloudriftsd",
		"time":    time.Now().UTC(),
	})
}

func (r *Router) setupState(w http.ResponseWriter, req *http.Request) {
	setup, _ := r.settings.Load()
	steps := panel.SetupSteps(setup)
	done := 0
	for _, step := range steps {
		if step.Done {
			done++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"completed": setup.SetupCompleted,
		"done":      done,
		"total":     len(steps),
		"steps":     steps,
	})
}

func (r *Router) getSetupSettings(w http.ResponseWriter, req *http.Request) {
	setup, err := r.settings.Load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, setup)
}

func (r *Router) putSetupSettings(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	var setup settings.SetupSettings
	if err := json.NewDecoder(req.Body).Decode(&setup); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	saved, err := r.settings.Save(setup)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (r *Router) applySetup(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	var setup settings.SetupSettings
	if err := json.NewDecoder(req.Body).Decode(&setup); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	result, err := r.panel.ApplySetup(setup)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	r.logger.Info("setup apply queued", "panel_url", result.Settings.PanelURL, "job", result.Job.ID)
	writeJSON(w, http.StatusAccepted, result)
}

func (r *Router) setupSuggestions(w http.ResponseWriter, req *http.Request) {
	panelURL := req.URL.Query().Get("panel_url")
	writeJSON(w, http.StatusOK, settings.Suggestions(panelURL))
}

func (r *Router) setupDNSCheck(w http.ResponseWriter, req *http.Request) {
	panelURL := req.URL.Query().Get("panel_url")
	dnsMode := req.URL.Query().Get("dns_mode")
	writeJSON(w, http.StatusOK, r.panel.CheckSetupDNS(panelURL, dnsMode))
}

func (r *Router) validateSetupSSO(w http.ResponseWriter, req *http.Request) {
	store := setupauth.NewStore(r.cfg.DataDir)
	token, err := store.Validate(req.PathValue("token"))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "expires_at": token.ExpiresAt})
}

func (r *Router) panelOverview(w http.ResponseWriter, req *http.Request) {
	overview, err := r.panel.Overview()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (r *Router) websites(w http.ResponseWriter, req *http.Request) {
	websites, err := r.panel.Websites()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"websites": websites})
}

func (r *Router) createWebsite(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	var input panel.CreateWebsiteRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	created, err := r.panel.CreateWebsite(input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	r.logger.Info("website provisioning queued", "domain", created.Website.Domain, "job", created.Job.ID)
	writeJSON(w, http.StatusCreated, created)
}

func (r *Router) website(w http.ResponseWriter, req *http.Request) {
	website, ok, err := r.panel.Website(req.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "website not found"})
		return
	}
	writeJSON(w, http.StatusOK, website)
}

func (r *Router) websiteDiagnostics(w http.ResponseWriter, req *http.Request) {
	diagnostics, ok, err := r.panel.Diagnostics(req.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "website not found"})
		return
	}
	writeJSON(w, http.StatusOK, diagnostics)
}

func (r *Router) retryWebsiteSSL(w http.ResponseWriter, req *http.Request) {
	result, ok, err := r.panel.RetrySSL(req.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "website not found"})
		return
	}
	r.logger.Info("website ssl retry", "domain", result.Website.Domain, "job", result.Job.ID, "ssl_status", result.Website.SSLStatus)
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) websiteBackups(w http.ResponseWriter, req *http.Request) {
	backups, ok, err := r.panel.WebsiteBackups(req.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "website not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": backups})
}

func (r *Router) createWebsiteBackup(w http.ResponseWriter, req *http.Request) {
	result, ok, err := r.panel.CreateBackup(req.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "website not found"})
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (r *Router) restoreWebsiteBackup(w http.ResponseWriter, req *http.Request) {
	result, ok, err := r.panel.RestoreBackup(req.PathValue("id"), req.PathValue("backup_id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "website or backup not found"})
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (r *Router) websiteFiles(w http.ResponseWriter, req *http.Request) {
	result, ok, err := r.panel.ListFiles(req.PathValue("id"), req.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "website not found"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) readWebsiteFile(w http.ResponseWriter, req *http.Request) {
	result, ok, err := r.panel.ReadFile(req.PathValue("id"), req.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "website not found"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) writeWebsiteFile(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	var input panel.WriteFileRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	result, ok, err := r.panel.WriteFile(req.PathValue("id"), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "website not found"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) websiteLogs(w http.ResponseWriter, req *http.Request) {
	lines := 200
	if raw := strings.TrimSpace(req.URL.Query().Get("lines")); raw != "" {
		var parsed int
		_, _ = fmt.Sscanf(raw, "%d", &parsed)
		if parsed > 0 {
			lines = parsed
		}
	}
	result, ok, err := r.panel.Logs(req.PathValue("id"), req.URL.Query().Get("kind"), lines)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "website not found"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) jobs(w http.ResponseWriter, req *http.Request) {
	jobs, err := r.panel.Jobs()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (r *Router) job(w http.ResponseWriter, req *http.Request) {
	job, ok, err := r.panel.Job(req.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "job not found"})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (r *Router) activity(w http.ResponseWriter, req *http.Request) {
	activity, err := r.panel.Activity()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": activity})
}

func (r *Router) roles(w http.ResponseWriter, req *http.Request) {
	roles, err := r.panel.Roles()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles})
}

func (r *Router) webservers(w http.ResponseWriter, req *http.Request) {
	webservers, err := r.panel.Webservers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webservers": webservers})
}

func (r *Router) switchWebserver(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	var input panel.SwitchWebserverRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	result, err := r.panel.SwitchWebserver(input.Webserver)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	r.logger.Info("webserver switch queued", "from", result.From, "to", result.To, "job", result.Job.ID)
	writeJSON(w, http.StatusAccepted, result)
}

func (r *Router) serverStats(w http.ResponseWriter, req *http.Request) {
	stats, err := systemstats.Collect()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (r *Router) dnsZones(w http.ResponseWriter, req *http.Request) {
	result, err := r.panel.DNSZones()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) setupDNSRole(w http.ResponseWriter, req *http.Request) {
	result, err := r.panel.SetupDNSRole()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	r.logger.Info("dns role setup queued", "job", result.Job.ID)
	writeJSON(w, http.StatusAccepted, result)
}

func (r *Router) createDNSZone(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	var input panel.CreateDNSZoneRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	result, err := r.panel.CreateDNSZone(input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	r.logger.Info("dns zone apply queued", "domain", result.Zone.Domain, "job", result.Job.ID)
	writeJSON(w, http.StatusAccepted, result)
}

func (r *Router) applyDNSZone(w http.ResponseWriter, req *http.Request) {
	result, ok, err := r.panel.ApplyDNSZone(req.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "dns zone not found"})
		return
	}
	r.logger.Info("dns zone apply queued", "domain", result.Zone.Domain, "job", result.Job.ID)
	writeJSON(w, http.StatusAccepted, result)
}

func (r *Router) upsertDNSRecord(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	var input panel.UpsertDNSRecordRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	result, ok, err := r.panel.UpsertDNSRecord(req.PathValue("id"), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "dns zone not found"})
		return
	}
	r.logger.Info("dns record apply queued", "domain", result.Zone.Domain, "record", result.Record.Name, "job", result.Job.ID)
	writeJSON(w, http.StatusAccepted, result)
}

func (r *Router) mailRole(w http.ResponseWriter, req *http.Request) {
	result, err := r.panel.MailRole()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) setupMailRole(w http.ResponseWriter, req *http.Request) {
	result, err := r.panel.SetupMailRole()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	r.logger.Info("mail role setup queued", "job", result.Job.ID)
	writeJSON(w, http.StatusAccepted, result)
}

func (r *Router) frontend(w http.ResponseWriter, req *http.Request) {
	if strings.HasPrefix(req.URL.Path, "/api/") {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "api route not found"})
		return
	}
	clean := filepath.Clean(strings.TrimPrefix(req.URL.Path, "/"))
	if clean == "." {
		clean = "index.html"
	}
	target := filepath.Join(r.cfg.WebDir, clean)
	if rel, err := filepath.Rel(r.cfg.WebDir, target); err != nil || strings.HasPrefix(rel, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid path"})
		return
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		target = filepath.Join(r.cfg.WebDir, "index.html")
	}
	if _, err := os.Stat(target); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "frontend not built", "web_dir": r.cfg.WebDir})
		return
	}
	http.ServeFile(w, req, target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
