package provisioning

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type webserverEngine struct {
	ID          string
	Name        string
	Service     string
	ValidateBin string
}

func (a *applier) activeWebserver() webserverEngine {
	return webserverEngineFor(a.spec.Webserver)
}

func webserverEngineFor(id string) webserverEngine {
	id = strings.ToLower(strings.TrimSpace(id))
	switch id {
	case "apache", "apache2":
		return webserverEngine{ID: "apache", Name: "Apache", Service: "apache2", ValidateBin: "apache2ctl"}
	case "openlitespeed", "ols":
		return webserverEngine{ID: "openlitespeed", Name: "OpenLiteSpeed", Service: "lsws", ValidateBin: "/usr/local/lsws/bin/lswsctrl"}
	case "litespeed", "lsws":
		return webserverEngine{ID: "litespeed", Name: "LiteSpeed", Service: "lsws", ValidateBin: "/usr/local/lsws/bin/lswsctrl"}
	default:
		return webserverEngine{ID: "nginx", Name: "Nginx", Service: "nginx", ValidateBin: "nginx"}
	}
}

func (a *applier) validateActiveWebserverInstalled() error {
	engine := a.activeWebserver()
	switch engine.ID {
	case "nginx":
		if _, err := exec.LookPath("nginx"); err != nil {
			return errors.New("nginx is missing; install nginx or switch the Cloudrifts webserver setting")
		}
	case "apache":
		for _, cmd := range []string{"apache2ctl", "a2ensite", "a2enmod"} {
			if _, err := exec.LookPath(cmd); err != nil {
				return fmt.Errorf("%s is missing; install apache2 or switch the Cloudrifts webserver setting", cmd)
			}
		}
	case "openlitespeed", "litespeed":
		if _, err := os.Stat("/usr/local/lsws/bin/lswsctrl"); err != nil {
			return fmt.Errorf("%s is not installed at /usr/local/lsws; install it first or switch the Cloudrifts webserver setting", engine.Name)
		}
	default:
		return fmt.Errorf("unsupported webserver %q", engine.ID)
	}
	return nil
}

func (a *applier) ensureWebserverHTTP() stepOutcome {
	engine := a.activeWebserver()
	poolName := safePoolName(a.spec.SiteID)
	socket := phpSocketPath(poolName)
	if a.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Would write %s HTTP vhost for %s, enable it, and validate config", engine.Name, a.spec.Domain)}
	}
	switch engine.ID {
	case "nginx":
		return a.ensureNginxHTTPAdapter(socket)
	case "apache":
		return a.ensureApacheHTTP(socket)
	case "openlitespeed", "litespeed":
		return a.ensureLiteSpeedHTTP(engine, socket)
	default:
		return stepOutcome{Err: fmt.Errorf("unsupported webserver %q", engine.ID)}
	}
}

func (a *applier) ensureNginxHTTPAdapter(socket string) stepOutcome {
	available := nginxAvailablePath(a.spec.SiteID)
	enabled := nginxEnabledPath(a.spec.SiteID)
	content := nginxHTTPConfig(a.spec, socket)
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
	if err := a.validateActiveWebserverConfig(); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: fmt.Sprintf("Nginx HTTP vhost enabled for %s and nginx -t passed", a.spec.Domain)}
}

func (a *applier) ensureApacheHTTP(socket string) stepOutcome {
	available := apacheAvailablePath(a.spec.SiteID)
	content := apacheHTTPConfig(a.spec, socket)
	old, hadOld := readIfExists(available)
	if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
		return stepOutcome{Err: err}
	}
	a.created = append(a.created, func() {
		_ = exec.Command("a2dissite", filepath.Base(available)).Run()
		if hadOld {
			_ = os.WriteFile(available, old, 0o644)
		} else {
			_ = os.Remove(available)
		}
	})
	a.rollback = append(a.rollback, "wrote Apache vhost "+available)
	for _, mod := range []string{"proxy", "proxy_fcgi", "setenvif", "rewrite", "headers", "ssl"} {
		if err := a.run("a2enmod", mod); err != nil {
			return stepOutcome{Err: err}
		}
	}
	if err := a.run("a2ensite", filepath.Base(available)); err != nil {
		return stepOutcome{Err: err}
	}
	if err := a.validateActiveWebserverConfig(); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: fmt.Sprintf("Apache HTTP vhost enabled for %s and apache2ctl configtest passed", a.spec.Domain)}
}

func (a *applier) ensureLiteSpeedHTTP(engine webserverEngine, socket string) stepOutcome {
	root := liteSpeedVHostRoot(a.spec.SiteID)
	conf := filepath.Join(root, "vhconf.conf")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return stepOutcome{Err: err}
	}
	old, hadOld := readIfExists(conf)
	if err := os.WriteFile(conf, []byte(liteSpeedHTTPConfig(a.spec, socket)), 0o644); err != nil {
		return stepOutcome{Err: err}
	}
	a.created = append(a.created, func() {
		if hadOld {
			_ = os.WriteFile(conf, old, 0o644)
		} else {
			_ = os.Remove(conf)
		}
	})
	a.rollback = append(a.rollback, "wrote "+engine.Name+" vhost "+conf)
	if err := a.ensureLiteSpeedMainMapping(engine); err != nil {
		return stepOutcome{Err: err}
	}
	if err := a.validateActiveWebserverConfig(); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: fmt.Sprintf("%s vhost written for %s and control command passed", engine.Name, a.spec.Domain)}
}

func (a *applier) ensureLiteSpeedMainMapping(engine webserverEngine) error {
	mainConf := "/usr/local/lsws/conf/httpd_config.conf"
	b, err := os.ReadFile(mainConf)
	if err != nil {
		return err
	}
	pool := safePoolName(a.spec.SiteID)
	marker := "cloudrifts-" + pool
	text := string(b)
	if strings.Contains(text, marker) && strings.Contains(text, a.spec.Domain) {
		return nil
	}
	appendBlock := fmt.Sprintf(`
# Cloudrifts managed vhost: %s
virtualHost %s {
  vhRoot                  %s
  configFile              $SERVER_ROOT/conf/vhosts/%s/vhconf.conf
  allowSymbolLink         1
  enableScript            1
  restrained              1
}

listener Cloudrifts-%s-80 {
  address                 *:80
  secure                  0
  map                     %s %s
}
`, a.spec.Domain, marker, a.spec.HomeDir, marker, pool, marker, a.spec.Domain)
	backup := mainConf + ".cloudrifts.bak"
	_ = os.WriteFile(backup, b, 0o644)
	a.created = append(a.created, func() { _ = os.WriteFile(mainConf, b, 0o644) })
	return os.WriteFile(mainConf, append(b, []byte(appendBlock)...), 0o644)
}

func (a *applier) validateActiveWebserverConfig() error {
	engine := a.activeWebserver()
	switch engine.ID {
	case "nginx":
		return a.run("nginx", "-t")
	case "apache":
		return a.run("apache2ctl", "configtest")
	case "openlitespeed", "litespeed":
		return a.run("/usr/local/lsws/bin/lswsctrl", "status")
	default:
		return fmt.Errorf("unsupported webserver %q", engine.ID)
	}
}

func (a *applier) reloadActiveWebserver() error {
	engine := a.activeWebserver()
	switch engine.ID {
	case "nginx":
		return a.run("systemctl", "reload", "nginx")
	case "apache":
		return a.run("systemctl", "reload", "apache2")
	case "openlitespeed", "litespeed":
		return a.run("/usr/local/lsws/bin/lswsctrl", "restart")
	default:
		return fmt.Errorf("unsupported webserver %q", engine.ID)
	}
}

func (a *applier) activateSSLVHost(certPath, keyPath string) error {
	engine := a.activeWebserver()
	socket := phpSocketPath(safePoolName(a.spec.SiteID))
	switch engine.ID {
	case "nginx":
		available := nginxAvailablePath(a.spec.SiteID)
		if err := os.WriteFile(available, []byte(nginxSSLConfig(a.spec, socket, certPath, keyPath)), 0o644); err != nil {
			return err
		}
	case "apache":
		available := apacheAvailablePath(a.spec.SiteID)
		if err := os.WriteFile(available, []byte(apacheSSLConfig(a.spec, socket, certPath, keyPath)), 0o644); err != nil {
			return err
		}
	case "openlitespeed", "litespeed":
		conf := filepath.Join(liteSpeedVHostRoot(a.spec.SiteID), "vhconf.conf")
		if err := os.WriteFile(conf, []byte(liteSpeedSSLConfig(a.spec, socket, certPath, keyPath)), 0o644); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported webserver %q", engine.ID)
	}
	if err := a.validateActiveWebserverConfig(); err != nil {
		return err
	}
	return a.reloadActiveWebserver()
}

func webserverDisplayName(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "apache", "apache2":
		return "Apache"
	case "openlitespeed", "ols":
		return "OpenLiteSpeed"
	case "litespeed", "lsws":
		return "LiteSpeed"
	default:
		return "Nginx"
	}
}

func apacheAvailablePath(siteID string) string {
	return "/etc/apache2/sites-available/cloudrifts-" + safePoolName(siteID) + ".conf"
}

func liteSpeedVHostRoot(siteID string) string {
	return "/usr/local/lsws/conf/vhosts/cloudrifts-" + safePoolName(siteID)
}

func apacheHTTPConfig(spec WebsiteSpec, socket string) string {
	return fmt.Sprintf(`<VirtualHost *:80>
    ServerName %s
    DocumentRoot %s

    ErrorLog %s
    CustomLog %s combined

    <Directory %s>
        Options FollowSymLinks
        AllowOverride All
        Require all granted
        DirectoryIndex index.php index.html index.htm
    </Directory>

    Alias /.well-known/acme-challenge/ %s/.well-known/acme-challenge/
    <Directory %s/.well-known/acme-challenge/>
        Require all granted
    </Directory>

    <FilesMatch "\\.php$">
        SetHandler "proxy:unix:%s|fcgi://localhost/"
    </FilesMatch>
</VirtualHost>
`, spec.Domain, spec.DocumentRoot, filepath.Join(spec.LogDir, "error.log"), filepath.Join(spec.LogDir, "access.log"), spec.DocumentRoot, spec.DocumentRoot, spec.DocumentRoot, socket)
}

func apacheSSLConfig(spec WebsiteSpec, socket string, certPath string, keyPath string) string {
	return fmt.Sprintf(`<VirtualHost *:80>
    ServerName %s
    DocumentRoot %s
    Alias /.well-known/acme-challenge/ %s/.well-known/acme-challenge/
    RewriteEngine On
    RewriteCond %%{REQUEST_URI} !^/\.well-known/acme-challenge/
    RewriteRule ^ https://%%{HTTP_HOST}%%{REQUEST_URI} [R=301,L]
</VirtualHost>

<VirtualHost *:443>
    ServerName %s
    DocumentRoot %s

    ErrorLog %s
    CustomLog %s combined

    SSLEngine on
    SSLCertificateFile %s
    SSLCertificateKeyFile %s

    <Directory %s>
        Options FollowSymLinks
        AllowOverride All
        Require all granted
        DirectoryIndex index.php index.html index.htm
    </Directory>

    <FilesMatch "\\.php$">
        SetHandler "proxy:unix:%s|fcgi://localhost/"
    </FilesMatch>
</VirtualHost>
`, spec.Domain, spec.DocumentRoot, spec.DocumentRoot, spec.Domain, spec.DocumentRoot, filepath.Join(spec.LogDir, "error.log"), filepath.Join(spec.LogDir, "access.log"), certPath, keyPath, spec.DocumentRoot, socket)
}

func liteSpeedHTTPConfig(spec WebsiteSpec, socket string) string {
	return fmt.Sprintf(`docRoot                   %s
vhDomain                  %s
enableGzip                1
index  {
  useServer               0
  indexFiles              index.php, index.html, index.htm
}
errorlog %s {
  useServer               0
  logLevel                WARN
}
accesslog %s {
  useServer               0
  rollingSize             10M
}
context / {
  type                    NULL
  location                %s
  allowBrowse             1
  rewrite  {
    enable                1
    rules                 <<<END_rules
      RewriteFile .htaccess
END_rules
  }
}
extprocessor cloudrifts-php {
  type                    proxy
  address                 uds://%s
  maxConns                35
  initTimeout             60
  retryTimeout            0
}
scriptHandler  {
  add                     lsapi:cloudrifts-php php
}
`, spec.DocumentRoot, spec.Domain, filepath.Join(spec.LogDir, "error.log"), filepath.Join(spec.LogDir, "access.log"), spec.DocumentRoot, socket)
}

func liteSpeedSSLConfig(spec WebsiteSpec, socket string, certPath string, keyPath string) string {
	return liteSpeedHTTPConfig(spec, socket) + fmt.Sprintf(`
ssl  {
  keyFile                 %s
  certFile                %s
  certChain               1
}
`, keyPath, certPath)
}
