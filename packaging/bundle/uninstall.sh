#!/usr/bin/env bash
set -euo pipefail

DATA_DIR="/var/lib/cloudrifts"
ETC_DIR="/etc/cloudrifts"
LOG_DIR="/var/log/cloudrifts"
WEB_DIR="/usr/local/share/cloudrifts"
SERVICE_FILE="/etc/systemd/system/cloudriftsd.service"
SUDOERS_FILE="/etc/sudoers.d/cloudrifts-agent"

purge=false
keep_packages=true

usage() {
  cat <<USAGE
Cloudrifts uninstaller

Usage:
  sudo ./uninstall.sh [--purge] [--remove-packages]

Options:
  --purge            Remove Cloudrifts state, logs, generated site configs, site users, and managed site homes.
  --remove-packages  Also remove packages installed by the test installer where possible.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --purge) purge=true ;;
    --remove-packages) keep_packages=false ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

if [[ ${EUID} -ne 0 ]]; then
  echo "Run as root: sudo ./uninstall.sh [--purge]"
  exit 1
fi

log() { printf '→ %s\n' "$*"; }
remove_file() { rm -f "$@" 2>/dev/null || true; }
remove_dir() { rm -rf "$@" 2>/dev/null || true; }

collect_site_cleanup() {
  [[ -f "$DATA_DIR/panel-state.json" ]] || return 0
  command -v python3 >/dev/null 2>&1 || return 0
  python3 - "$DATA_DIR/panel-state.json" <<'PY'
import json, os, re, shlex, sys
path = sys.argv[1]
try:
    data = json.load(open(path))
except Exception:
    sys.exit(0)
for site in data.get("websites", []):
    site_id = str(site.get("id", ""))
    user = str(site.get("system_user", ""))
    home = str(site.get("home_dir", ""))
    safe_id = re.sub(r"[^a-zA-Z0-9_-]", "_", site_id)[:48]
    if safe_id:
        print("site_id=" + shlex.quote(safe_id))
    if user.startswith("cr_") or user.startswith("cloudrifts_"):
        print("user=" + shlex.quote(user))
    if home.startswith("/home/cr_") or home.startswith("/home/cloudrifts_"):
        print("home=" + shlex.quote(home))
PY
}

cleanup_generated_assets() {
  log "removing Cloudrifts generated configs"
  remove_file /etc/nginx/sites-enabled/cloudrifts-*.conf /etc/nginx/sites-available/cloudrifts-*.conf
  remove_file /etc/apache2/sites-enabled/cloudrifts-*.conf /etc/apache2/sites-available/cloudrifts-*.conf
  remove_file /etc/php/*/fpm/pool.d/site_*.conf /etc/php/*/fpm/pool.d/cloudrifts_*.conf
  remove_dir /usr/local/lsws/conf/vhosts/cloudrifts-*
  remove_file /etc/powerdns/pdns.d/cloudrifts-sqlite3.conf
  remove_file /etc/dovecot/conf.d/99-cloudrifts-mail.conf
  remove_file /etc/postfix/cloudrifts-*.cf
  remove_dir /home/cr_site_* /home/cloudrifts_site_*

  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    key="${line%%=*}"
    value="${line#*=}"
    case "$key" in
      site_id)
        remove_file "/etc/nginx/sites-enabled/cloudrifts-${value}.conf" "/etc/nginx/sites-available/cloudrifts-${value}.conf"
        remove_file "/etc/apache2/sites-enabled/cloudrifts-${value}.conf" "/etc/apache2/sites-available/cloudrifts-${value}.conf"
        remove_dir "/usr/local/lsws/conf/vhosts/cloudrifts-${value}"
        remove_file /etc/php/*/fpm/pool.d/"${value}.conf"
        ;;
      user)
        if id "$value" >/dev/null 2>&1; then
          userdel -r "$value" >/dev/null 2>&1 || userdel "$value" >/dev/null 2>&1 || true
        fi
        ;;
      home)
        remove_dir "$value"
        ;;
    esac
  done < <(collect_site_cleanup)

  if getent group cloudrifts-sites >/dev/null 2>&1; then
    groupdel cloudrifts-sites >/dev/null 2>&1 || true
  fi

  systemctl reload nginx >/dev/null 2>&1 || true
  systemctl reload apache2 >/dev/null 2>&1 || true
  for unit in /lib/systemd/system/php*-fpm.service; do
    [[ -e "$unit" ]] || continue
    systemctl reload "$(basename "$unit")" >/dev/null 2>&1 || true
  done
  systemctl disable --now pdns >/dev/null 2>&1 || true
}

log "stopping Cloudrifts"
systemctl disable --now cloudriftsd >/dev/null 2>&1 || true
remove_file "$SERVICE_FILE" "$SUDOERS_FILE"
systemctl daemon-reload || true

log "removing Cloudrifts binaries and web assets"
remove_file /usr/local/bin/cloudriftsd /usr/local/bin/cloudrifts-agent /usr/local/bin/cloudrifts-filed /usr/local/bin/cloudrifts /usr/local/bin/cloudriftsctl
remove_dir "$WEB_DIR"

if [[ "$purge" == true ]]; then
  cleanup_generated_assets
  log "purging Cloudrifts state"
  remove_dir "$DATA_DIR" "$ETC_DIR" "$LOG_DIR"
  userdel cloudrifts >/dev/null 2>&1 || true
fi

if [[ "$keep_packages" == false ]]; then
  log "removing installer packages"
  export DEBIAN_FRONTEND=noninteractive
  apt-get remove -y nginx apache2 php-fpm php-cli php-mysql php-curl php-xml php-mbstring php-zip php-gd php-intl php-bcmath mariadb-server pdns-server pdns-backend-sqlite3 postfix dovecot-imapd dovecot-lmtpd certbot >/dev/null || true
  apt-get autoremove -y >/dev/null || true
fi

if [[ "$purge" == true ]]; then
  echo "Cloudrifts removed with data purge."
else
  echo "Cloudrifts removed. Data kept under $DATA_DIR. Use --purge to remove Cloudrifts data and generated site assets."
fi
