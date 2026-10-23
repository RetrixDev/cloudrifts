# Cloudrifts Feature Status

Cloudrifts is a Phase 1 single-server hosting panel. This page describes what the current public test release is designed to do and where the boundaries are.

## Implemented in the Current Build

| Area | Status |
| --- | --- |
| Ubuntu 24.04 bundle installer | Available |
| React/Vite browser panel | Available |
| Go API daemon | Available |
| Privileged server agent | Available |
| One-time setup URL | Available |
| WordPress site creation | Available |
| WooCommerce site creation | Available |
| Plain PHP site creation | Available |
| MariaDB database/user creation | Available |
| PHP-FPM pool creation | Available |
| Nginx vhost rendering | Available |
| Apache vhost rendering | Available |
| OpenLiteSpeed/LiteSpeed config paths | Available when the target server is installed |
| Webserver switching workflow | Available with validation and rollback steps |
| Let's Encrypt SSL attempt and retry | Available |
| DNS preflight and required record hints | Available |
| Website diagnostics | Available |
| File list/read/write/create | Available |
| Website logs | Available |
| Local backup creation | Available |
| Privileged restore job | Available |
| PowerDNS managed DNS setup | Available |
| DNS zones and record apply jobs | Available |
| Postfix/Dovecot role setup/status | Available |
| Job trail with steps, issues, logs, and recovery hints | Available |

## Phase 1 Scope

Cloudrifts Phase 1 is intentionally focused:

- One Ubuntu 24.04 server.
- WordPress, WooCommerce, and PHP websites.
- One active webserver engine at a time.
- MariaDB for database-backed sites.
- Optional managed DNS on the same VPS.
- Optional mail role health and setup before full mailbox management.

## Not Ready Yet

These are not presented as finished production features in the current release:

- Multi-server clusters.
- Full mailbox/user management.
- DKIM, DMARC automation, and webmail provisioning.
- Complete production authentication and team permission hardening.
- Remote backup providers.
- Fine-grained restore browsing.
- One-click migration from other panels.

## Positioning

Cloudrifts is useful for people searching for a hosting control panel, VPS control panel, WordPress hosting panel, PHP hosting panel, cPanel alternative, CyberPanel alternative, or CloudPanel-style panel for a single VPS.

