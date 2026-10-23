#!/usr/bin/env bash
set -euo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_PREFIX="/usr/local"
DATA_DIR="/var/lib/cloudrifts"
ETC_DIR="/etc/cloudrifts"
LOG_DIR="/var/log/cloudrifts"
WEB_DIR="$INSTALL_PREFIX/share/cloudrifts/web"
SERVICE_FILE="/etc/systemd/system/cloudriftsd.service"
SUDOERS_FILE="/etc/sudoers.d/cloudrifts-agent"
INSTALL_LOG="${LOG_DIR}/install-$(date -u +%Y%m%dT%H%M%SZ).log"

NO_UI=0
VERBOSE=0
PREVIEW_UI=0
for arg in "$@"; do
  case "$arg" in
    --no-ui) NO_UI=1 ;;
    --verbose) VERBOSE=1 ;;
    --preview-ui) PREVIEW_UI=1 ;;
    -h|--help)
      cat <<HELP
Cloudrifts installer

Usage:
  sudo ./install.sh [--no-ui] [--verbose]
  ./install.sh --preview-ui

Options:
  --no-ui       Use plain installer output.
  --verbose     Disable quiet UI and print command output normally.
  --preview-ui  Preview the terminal installer UI without changing the server.
HELP
      exit 0
      ;;
    *) echo "Unknown option: $arg" >&2; exit 2 ;;
  esac
done

if (( PREVIEW_UI == 0 && EUID != 0 )); then
  echo "Run as root: sudo ./install.sh"
  exit 1
fi

mkdir -p "$LOG_DIR" 2>/dev/null || true
touch "$INSTALL_LOG" 2>/dev/null || INSTALL_LOG="/tmp/cloudrifts-install-$(date -u +%Y%m%dT%H%M%SZ).log"
chmod 0644 "$INSTALL_LOG" 2>/dev/null || true

UI_ENABLED=0
if (( NO_UI == 0 && VERBOSE == 0 )) && [[ -t 1 ]] && [[ "${TERM:-}" != "dumb" ]]; then
  cols="$(tput cols 2>/dev/null || echo 0)"
  if [[ "$cols" =~ ^[0-9]+$ ]] && (( cols >= 72 )); then
    UI_ENABLED=1
  fi
fi

C_RESET=$'\033[0m'
C_BOLD=$'\033[1m'
C_DIM=$'\033[2m'
C_VIOLET=$'\033[38;2;132;110;255m'
C_CYAN=$'\033[38;2;70;205;228m'
C_MINT=$'\033[38;2;78;220;153m'
C_AMBER=$'\033[38;2;235;181;84m'
C_ROSE=$'\033[38;2;240;92;120m'
C_MUTED=$'\033[38;2;132;146;166m'
C_DIMTXT=$'\033[38;2;78;93;116m'
C_LINE=$'\033[38;2;58;70;94m'
C_PANEL=$'\033[48;2;14;19;29m'
C_PANEL2=$'\033[48;2;17;23;34m'

CURRENT_PERCENT=0
SPINNER_INDEX=0
START_TS="$(date +%s)"
PHASE_INDEX=0
ACTIVE_TITLE="Starting"
ACTIVE_SUBTITLE="quiet installer telemetry"
ACTIVE_OP="Preparing installer"
INSTALL_STATUS="starting"
CHECKS_DONE=0
CHECKS_TOTAL=13
ROLE_NGINX="queued"
ROLE_PHP="queued"
ROLE_DB="queued"
ROLE_DNS="queued"
ROLE_SSL="queued"
SETUP_URL=""
SERVER_IP=""
LAST_ERROR=""
SUCCESS_VIEW=0
UI_RENDERED=0

cleanup_ui() {
  if (( UI_ENABLED == 1 )); then
    printf '\033[?25h\n'
  fi
}
trap cleanup_ui EXIT

plain() {
  if (( UI_ENABLED == 0 )); then
    printf '%s\n' "$*"
  fi
}

repeat_char() {
  local char="$1" count="$2" out=""
  for ((i=0; i<count; i++)); do out+="$char"; done
  printf '%s' "$out"
}

strip_ansi_len() {
  local raw="$1"
  raw="$(printf '%s' "$raw" | sed -E $'s/\x1B\[[0-9;?]*[A-Za-z]//g')"
  printf '%s' "${#raw}"
}

pad_line() {
  local content="$1" width=78 len pad
  len="$(strip_ansi_len "$content")"
  if (( len < width )); then
    pad="$(repeat_char ' ' $((width-len)))"
  else
    pad=""
  fi
  printf '│%b%s│\n' "$content" "$pad"
}

progress_bar() {
  local pct="$1" width="${2:-46}" color="${3:-$C_VIOLET}" filled empty
  (( pct < 0 )) && pct=0
  (( pct > 100 )) && pct=100
  filled=$((pct * width / 100))
  empty=$((width-filled))
  printf '%b%s%b%s%b' "$color" "$(repeat_char '━' "$filled")" "$C_LINE" "$(repeat_char '─' "$empty")" "$C_RESET"
}

mini_bar() {
  local pct="$1" color="$2" width=12 filled empty
  filled=$((pct * width / 100))
  empty=$((width-filled))
  printf '%b%s%b%s%b' "$color" "$(repeat_char '━' "$filled")" "$C_LINE" "$(repeat_char '─' "$empty")" "$C_RESET"
}

pulse_bars() {
  local frame="$1" colors heights idx h color out=""
  colors=("$C_VIOLET" "$C_CYAN" "$C_MINT" "$C_AMBER")
  heights=(▂ ▃ ▅ ▇ ▆ ▄ ▁ ▃ ▆ ▇ ▅ ▂)
  for idx in "${!heights[@]}"; do
    h="${heights[$(((idx + frame) % ${#heights[@]}))]}"
    color="${colors[$((idx % ${#colors[@]}))]}"
    out+="${color}${h}${C_RESET} "
  done
  printf '%b' "$out"
}

phase_dot() {
  local idx="$1" label="$2" color="$C_DIMTXT" marker="○"
  if (( idx < PHASE_INDEX )); then
    color="$C_MINT"; marker="●"
  elif (( idx == PHASE_INDEX )); then
    color="$C_VIOLET"; marker="●"
  fi
  printf '%b%s%b %s' "$color" "$marker" "$C_RESET" "$label"
}

role_status_color() {
  case "$1" in
    ready|active|done) printf '%s' "$C_MINT" ;;
    writing|installing|running) printf '%s' "$C_CYAN" ;;
    next|queued|waiting) printf '%s' "$C_DIMTXT" ;;
    failed) printf '%s' "$C_ROSE" ;;
    *) printf '%s' "$C_MUTED" ;;
  esac
}

role_line() {
  local name="$1" status="$2" color
  color="$(role_status_color "$status")"
  printf '%b●%b %-10s %b%-9s%b' "$color" "$C_RESET" "$name" "$color" "$status" "$C_RESET"
}

elapsed_label() {
  local now elapsed mm ss
  now="$(date +%s)"
  elapsed=$((now-START_TS))
  mm=$((elapsed/60))
  ss=$((elapsed%60))
  printf '%02d:%02d' "$mm" "$ss"
}

ui_render() {
  (( UI_ENABLED == 1 )) || return 0
  local spinner frames status_color role1 role2 role3 role4 pulse elapsed mode_label
  frames=(⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏)
  spinner="${frames[$((SPINNER_INDEX % ${#frames[@]}))]}"
  pulse="$(pulse_bars "$SPINNER_INDEX")"
  elapsed="$(elapsed_label)"
  status_color="$C_VIOLET"
  mode_label="quiet mode"
  [[ "$INSTALL_STATUS" == "ready" ]] && status_color="$C_MINT" && mode_label="ready" && spinner="✓"
  [[ "$INSTALL_STATUS" == "failed" ]] && status_color="$C_ROSE" && mode_label="needs attention"

  role1="$(role_line nginx "$ROLE_NGINX")"
  role2="$(role_line php-fpm "$ROLE_PHP")"
  role3="$(role_line mariadb "$ROLE_DB")"
  role4="$(role_line powerdns "$ROLE_DNS")"

  if (( UI_RENDERED == 0 )); then
    printf '\033[?25l\033[H\033[2J'
    UI_RENDERED=1
  else
    printf '\033[H'
  fi

  printf '%b╭%s╮%b\n' "$C_LINE" "$(repeat_char '─' 78)" "$C_RESET"
  pad_line "  ${C_ROSE}●${C_RESET} ${C_AMBER}●${C_RESET} ${C_MINT}●${C_RESET}  ${C_MUTED}cloudrifts install${C_RESET}                                  ${C_DIMTXT}${mode_label}${C_RESET}  "
  printf '%b├%s┤%b\n' "$C_LINE" "$(repeat_char '─' 78)" "$C_RESET"
  pad_line "  $(phase_dot 0 scan)     $(phase_dot 1 roles)     $(phase_dot 2 secure)     $(phase_dot 3 open)"
  pad_line ""
  pad_line "  ${C_BOLD}Cloudrifts${C_RESET}  ${C_MUTED}${ACTIVE_TITLE}${C_RESET}                 ${status_color}${spinner} ${INSTALL_STATUS}${C_RESET} ${C_MUTED}${CURRENT_PERCENT}%${C_RESET}"
  pad_line "  $(progress_bar "$CURRENT_PERCENT" 48 "$status_color")"
  pad_line ""

  if (( SUCCESS_VIEW == 1 )); then
    pad_line "  ${C_PANEL2}  ${C_MINT}${C_BOLD}setup handoff ready${C_RESET}"
    pad_line "  ${C_PANEL2}  ${C_MUTED}open once, finish domain + DNS + SSL from browser setup${C_RESET}"
    pad_line ""
    pad_line "  ${C_MUTED}command${C_RESET} ${C_BOLD}cloudrifts${C_RESET}          ${C_MUTED}sso${C_RESET} ${C_BOLD}cloudrifts sso${C_RESET}"
    pad_line "  ${C_MUTED}setup${C_RESET}   ${C_BOLD}${SETUP_URL}${C_RESET}"
    pad_line "  ${C_MUTED}panel${C_RESET}   http://${SERVER_IP}:8080        ${C_MUTED}api${C_RESET}  http://${SERVER_IP}:8080/api/health"
    pad_line ""
    pad_line "  ${C_PANEL}${role1}${C_RESET}        ${C_PANEL}${role2}${C_RESET}"
    pad_line "  ${C_PANEL}${role3}${C_RESET}        ${C_PANEL}${role4}${C_RESET}"
    pad_line ""
    pad_line "  ${C_PANEL2} elapsed ${C_BOLD}${elapsed}${C_RESET} ${C_MUTED}$(mini_bar 100 "$C_MINT")${C_RESET}   ${C_PANEL2} checks ${C_BOLD}${CHECKS_DONE}/${CHECKS_TOTAL}${C_RESET} ${C_MUTED}$(mini_bar 100 "$C_CYAN")${C_RESET}"
    pad_line "  ${C_PANEL2}${C_MINT} installed ${C_RESET}${C_MUTED}logs saved at ${INSTALL_LOG}${C_RESET}"
  else
    pad_line "  ${C_PANEL2}  ${C_BOLD}${ACTIVE_OP}${C_RESET}"
    pad_line "  ${C_PANEL2}  ${C_MUTED}$(progress_bar "$CURRENT_PERCENT" 34 "$C_CYAN")${C_RESET}      ${pulse}"
    pad_line ""
    pad_line "  ${C_PANEL}${role1}${C_RESET}        ${C_PANEL}${role2}${C_RESET}"
    pad_line "  ${C_PANEL}${role3}${C_RESET}        ${C_PANEL}${role4}${C_RESET}"
    pad_line ""
    pad_line "  ${C_PANEL2} elapsed ${C_BOLD}${elapsed}${C_RESET} ${C_MUTED}$(mini_bar "$CURRENT_PERCENT" "$C_VIOLET")${C_RESET}   ${C_PANEL2} checks ${C_BOLD}${CHECKS_DONE}/${CHECKS_TOTAL}${C_RESET} ${C_MUTED}$(mini_bar $(( CHECKS_DONE * 100 / CHECKS_TOTAL )) "$C_CYAN")${C_RESET}"
    pad_line "  ${C_PANEL2}${C_MINT} rollback safe ${C_RESET}${C_MUTED}logs hidden unless needed${C_RESET}"
    pad_line ""
    if [[ -n "$LAST_ERROR" ]]; then
      pad_line "  ${C_ROSE}failed${C_RESET} ${LAST_ERROR}"
      pad_line "  ${C_MUTED}log:${C_RESET} ${INSTALL_LOG}"
    else
      pad_line "  ${C_MUTED}log:${C_RESET} ${INSTALL_LOG}"
    fi
  fi
  printf '%b╰%s╯%b\n' "$C_LINE" "$(repeat_char '─' 78)" "$C_RESET"
}

animate_to() {
  local target="$1"
  (( target < CURRENT_PERCENT )) && target="$CURRENT_PERCENT"
  while (( CURRENT_PERCENT < target )); do
    CURRENT_PERCENT=$((CURRENT_PERCENT+1))
    SPINNER_INDEX=$((SPINNER_INDEX+1))
    ui_render
    sleep 0.045
  done
  ui_render
}

set_role_after_step() {
  local op="$1"
  case "$op" in
    *package*|*roles*) ROLE_NGINX="ready"; ROLE_PHP="ready"; ROLE_DB="ready"; ROLE_DNS="active"; ROLE_SSL="ready" ;;
    *WP-CLI*) ROLE_SSL="ready" ;;
    *service*) ROLE_NGINX="ready"; ROLE_PHP="ready"; ROLE_DB="ready"; ROLE_DNS="active" ;;
  esac
}

fail_install() {
  local message="$1" rc="${2:-1}"
  INSTALL_STATUS="failed"
  LAST_ERROR="$message"
  ui_render
  cleanup_ui
  printf '\nCloudrifts install failed: %s\n' "$message" >&2
  printf 'Log: %s\n' "$INSTALL_LOG" >&2
  printf 'Recovery: tail -n 120 %q\n' "$INSTALL_LOG" >&2
  exit "$rc"
}

run_step() {
  local phase="$1" target="$2" op="$3"; shift 3
  PHASE_INDEX="$phase"
  ACTIVE_TITLE="$op"
  ACTIVE_OP="$op"
  INSTALL_STATUS="running"
  ui_render
  plain "→ $op"
  {
    printf '\n== %s ==\n' "$op"
    date -u +%Y-%m-%dT%H:%M:%SZ
  } >>"$INSTALL_LOG"

  set +e
  ( "$@" ) >>"$INSTALL_LOG" 2>&1 &
  local pid=$! rc=0 hold=$(( target > 0 ? target - 1 : target ))
  while kill -0 "$pid" >/dev/null 2>&1; do
    if (( CURRENT_PERCENT < hold )); then
      CURRENT_PERCENT=$((CURRENT_PERCENT+1))
    fi
    SPINNER_INDEX=$((SPINNER_INDEX+1))
    ui_render
    sleep 0.22
  done
  wait "$pid"
  rc=$?
  set -e
  if (( rc != 0 )); then
    fail_install "$op" "$rc"
  fi
  CHECKS_DONE=$((CHECKS_DONE+1))
  set_role_after_step "$op"
  animate_to "$target"
}

step_preflight_bundle() {
  [[ -x "$SELF_DIR/bin/cloudriftsd" && -x "$SELF_DIR/bin/cloudrifts-agent" && -x "$SELF_DIR/bin/cloudrifts" && -d "$SELF_DIR/web" ]] || {
    echo "Installer bundle is incomplete. Expected bin/cloudriftsd, bin/cloudrifts-agent, bin/cloudrifts and web/ next to install.sh."
    exit 1
  }
}

step_preflight_os() {
  [[ -f /etc/os-release ]] || { echo "Unsupported OS: missing /etc/os-release"; exit 1; }
  # shellcheck disable=SC1091
  . /etc/os-release
  [[ "${ID}" == "ubuntu" && "${VERSION_ID}" == "24.04" ]] || {
    echo "Cloudrifts Phase 1 installer supports Ubuntu 24.04 only. Detected: ${PRETTY_NAME:-unknown}"
    exit 1
  }
  command -v systemctl >/dev/null 2>&1 || { echo "systemctl is required. Use a normal Ubuntu 24.04 VPS with systemd."; exit 1; }
}

step_prepare_apt() {
  export DEBIAN_FRONTEND=noninteractive
  local server_mailname
  server_mailname="$(hostname -f 2>/dev/null || hostname)"
  printf 'postfix postfix/mailname string %s\n' "$server_mailname" | debconf-set-selections || true
  printf 'postfix postfix/main_mailer_type select Internet Site\n' | debconf-set-selections || true
}

step_apt_update() {
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
}

step_install_packages() {
  export DEBIAN_FRONTEND=noninteractive
  apt-get install -y ca-certificates curl sudo nginx apache2 php-fpm php-cli php-mysql php-curl php-xml php-mbstring php-zip php-gd php-intl php-bcmath mariadb-server pdns-server pdns-backend-sqlite3 sqlite3 postfix dovecot-imapd dovecot-lmtpd ssl-cert certbot openssl tar unzip rsync
}

step_wp_cli() {
  if ! command -v wp >/dev/null 2>&1; then
    curl -fsSL https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar -o /usr/local/bin/wp
    chmod 0755 /usr/local/bin/wp
  fi
}

step_user_dirs() {
  if ! id cloudrifts >/dev/null 2>&1; then
    useradd --system --home "$DATA_DIR" --shell /usr/sbin/nologin cloudrifts
  fi
  install -d -o cloudrifts -g cloudrifts -m 0750 "$DATA_DIR" "$ETC_DIR" "$LOG_DIR" "$DATA_DIR/jobs" /backups
  install -d -o root -g root -m 0755 "$INSTALL_PREFIX/bin" "$INSTALL_PREFIX/share/cloudrifts" "$WEB_DIR"
}

step_install_files() {
  install -o root -g root -m 0755 "$SELF_DIR/bin/cloudriftsd" "$INSTALL_PREFIX/bin/cloudriftsd"
  install -o root -g root -m 0755 "$SELF_DIR/bin/cloudrifts-agent" "$INSTALL_PREFIX/bin/cloudrifts-agent"
  install -o root -g root -m 0755 "$SELF_DIR/bin/cloudrifts-filed" "$INSTALL_PREFIX/bin/cloudrifts-filed"
  install -o root -g root -m 0755 "$SELF_DIR/bin/cloudrifts" "$INSTALL_PREFIX/bin/cloudrifts"
  install -o root -g root -m 0755 "$SELF_DIR/bin/cloudriftsctl" "$INSTALL_PREFIX/bin/cloudriftsctl"
  rm -rf "$WEB_DIR"
  install -d -o root -g root -m 0755 "$WEB_DIR"
  cp -R "$SELF_DIR/web"/. "$WEB_DIR"/
  install -o root -g root -m 0644 "$SELF_DIR/systemd/cloudriftsd.service" "$SERVICE_FILE"
}

step_sudoers() {
  cat > "$SUDOERS_FILE" <<SUDOERS
Defaults:cloudrifts !requiretty
cloudrifts ALL=(root) NOPASSWD: /usr/local/bin/cloudrifts-agent apply-website *
cloudrifts ALL=(root) NOPASSWD: /usr/local/bin/cloudrifts-agent switch-webserver *
cloudrifts ALL=(root) NOPASSWD: /usr/local/bin/cloudrifts-agent restore-backup *
cloudrifts ALL=(root) NOPASSWD: /usr/local/bin/cloudrifts-agent setup-dns-role *
cloudrifts ALL=(root) NOPASSWD: /usr/local/bin/cloudrifts-agent apply-dns-zone *
cloudrifts ALL=(root) NOPASSWD: /usr/local/bin/cloudrifts-agent setup-mail-role *
cloudrifts ALL=(root) NOPASSWD: /usr/local/bin/cloudrifts-agent setup-panel *
SUDOERS
  chmod 0440 "$SUDOERS_FILE"
  visudo -cf "$SUDOERS_FILE" >/dev/null
}

step_services() {
  systemctl enable --now nginx >/dev/null || true
  systemctl disable --now apache2 >/dev/null 2>&1 || true
  systemctl enable --now mariadb >/dev/null || true
  for unit in /lib/systemd/system/php*-fpm.service; do
    [[ -e "$unit" ]] || continue
    systemctl enable --now "$(basename "$unit")" >/dev/null || true
  done
  systemctl disable --now pdns >/dev/null 2>&1 || true
  systemctl daemon-reload
  systemctl enable cloudriftsd >/dev/null
  systemctl restart cloudriftsd
}

step_firewall() {
  if command -v ufw >/dev/null 2>&1 && ufw status | grep -qi '^Status: active'; then
    ufw allow 8080/tcp >/dev/null || true
    ufw allow 80/tcp >/dev/null || true
    ufw allow 443/tcp >/dev/null || true
    ufw allow 53/tcp >/dev/null || true
    ufw allow 53/udp >/dev/null || true
    ufw allow 25/tcp >/dev/null || true
    ufw allow 465/tcp >/dev/null || true
    ufw allow 587/tcp >/dev/null || true
    ufw allow 143/tcp >/dev/null || true
    ufw allow 993/tcp >/dev/null || true
  fi
}

step_health() {
  local healthy=0
  for _ in $(seq 1 40); do
    if "$INSTALL_PREFIX/bin/cloudriftsctl" status --addr http://127.0.0.1:8080 >/dev/null 2>&1; then
      healthy=1
      break
    fi
    sleep 0.25
  done
  (( healthy == 1 )) || {
    echo "Cloudrifts service did not become healthy. Run: journalctl -u cloudriftsd -n 120 --no-pager"
    exit 1
  }
}


detect_setup_host() {
  local token public_ip route_ip host_ip

  if [[ -n "${CLOUDRIFTS_SETUP_HOST:-}" ]]; then
    printf '%s' "$CLOUDRIFTS_SETUP_HOST"
    return 0
  fi

  token="$(curl -fsS --max-time 1 -X PUT \
    -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' \
    http://169.254.169.254/latest/api/token 2>/dev/null || true)"
  if [[ -n "$token" ]]; then
    public_ip="$(curl -fsS --max-time 1 \
      -H "X-aws-ec2-metadata-token: $token" \
      http://169.254.169.254/latest/meta-data/public-ipv4 2>/dev/null || true)"
    if [[ "$public_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      printf '%s' "$public_ip"
      return 0
    fi
  fi

  public_ip="$(curl -fsS --max-time 2 https://api.ipify.org 2>/dev/null || true)"
  if [[ "$public_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    printf '%s' "$public_ip"
    return 0
  fi

  route_ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '/src/ {for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}' || true)"
  if [[ "$route_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    printf '%s' "$route_ip"
    return 0
  fi

  host_ip="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
  if [[ "$host_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    printf '%s' "$host_ip"
    return 0
  fi

  printf '127.0.0.1'
}

step_setup_url() {
  SERVER_IP="$(detect_setup_host)"
  SETUP_URL="$($INSTALL_PREFIX/bin/cloudriftsctl setup-url --host "$SERVER_IP" --port 8080 --data-dir "$DATA_DIR")"
  if [[ -z "$SETUP_URL" ]]; then
    echo "Could not create setup SSO URL. Run: cloudrifts sso --host <server-ip> --port 8080 --data-dir $DATA_DIR"
    exit 1
  fi
  printf '%s\n' "$SETUP_URL" >"$DATA_DIR/last-setup-url.txt"
  chown cloudrifts:cloudrifts "$DATA_DIR/last-setup-url.txt" "$DATA_DIR/setup-token.json" 2>/dev/null || true
  chmod 0640 "$DATA_DIR/setup-token.json" 2>/dev/null || true
}

load_setup_handoff() {
  SETUP_URL="$(cat "$DATA_DIR/last-setup-url.txt" 2>/dev/null || true)"
  SERVER_IP="$(printf '%s' "$SETUP_URL" | sed -E 's#^https?://([^/:]+).*#\1#')"
  if [[ -z "$SERVER_IP" || "$SERVER_IP" == "$SETUP_URL" ]]; then
    SERVER_IP="$(detect_setup_host)"
  fi
}

preview_step() {
  local seconds="$1"
  sleep "$seconds"
}

render_success() {
  PHASE_INDEX=4
  INSTALL_STATUS="ready"
  ACTIVE_TITLE="installed"
  ACTIVE_OP="Open setup"
  SUCCESS_VIEW=1
  ROLE_NGINX="ready"; ROLE_PHP="ready"; ROLE_DB="ready"; ROLE_DNS="active"; ROLE_SSL="ready"
  CHECKS_DONE="$CHECKS_TOTAL"
  animate_to 100
  cleanup_ui
  if (( UI_ENABLED == 1 )); then
    printf '\n'
    return 0
  fi
  cat <<MSG
Cloudrifts panel installed.

Open command center:
cloudrifts

Create login/setup link anytime:
cloudrifts sso

Current setup URL:
${SETUP_URL}

Panel:   http://${SERVER_IP}:8080
API:     http://${SERVER_IP}:8080/api/health
Data:    ${DATA_DIR}
Config:  ${ETC_DIR}
Logs:    ${LOG_DIR}
Install: ${INSTALL_LOG}
Service: systemctl status cloudriftsd

Firewall: opened panel, web, DNS, SMTP and IMAP ports only if UFW was active.
MSG
}

if (( UI_ENABLED == 1 )); then
  printf '\033[?25l'
fi

if (( PREVIEW_UI == 1 )); then
  CHECKS_TOTAL=6
  run_step 0 8 "preflight scan" preview_step 0.35
  run_step 0 15 "network ports" preview_step 0.35
  ROLE_NGINX="installing"; ROLE_PHP="installing"; ROLE_DB="installing"; ROLE_DNS="installing"; ROLE_SSL="next"
  run_step 1 42 "installing role fabric" preview_step 1.8
  ROLE_NGINX="ready"; ROLE_PHP="ready"; ROLE_DB="ready"; ROLE_DNS="writing"; ROLE_SSL="next"
  run_step 1 64 "PowerDNS backend" preview_step 1.4
  run_step 2 82 "panel route + firewall" preview_step 1.0
  run_step 2 92 "DNS and SSL readiness" preview_step 0.8
  SERVER_IP="SERVER-IP"
  SETUP_URL="http://SERVER-IP:8080/setup/sso/••••••••"
  render_success
  exit 0
fi

plain "Cloudrifts installer started. Log: $INSTALL_LOG"
run_step 0 5 "checking installer bundle" step_preflight_bundle
run_step 0 10 "checking Ubuntu 24.04" step_preflight_os
run_step 0 15 "preparing package manager" step_prepare_apt
ROLE_NGINX="installing"; ROLE_PHP="installing"; ROLE_DB="installing"; ROLE_DNS="installing"; ROLE_SSL="next"
run_step 1 25 "updating package index" step_apt_update
run_step 1 52 "installing server roles" step_install_packages
ROLE_NGINX="ready"; ROLE_PHP="ready"; ROLE_DB="ready"; ROLE_DNS="active"; ROLE_SSL="ready"
run_step 1 58 "installing WP-CLI" step_wp_cli
run_step 1 64 "creating Cloudrifts user and paths" step_user_dirs
run_step 1 70 "installing panel binaries" step_install_files
run_step 2 76 "installing privileged agent policy" step_sudoers
run_step 2 84 "starting Cloudrifts services" step_services
run_step 2 90 "opening firewall ports" step_firewall
run_step 2 95 "checking panel health" step_health
run_step 3 98 "creating setup handoff URL" step_setup_url
load_setup_handoff
render_success
