# Install Cloudrifts

Cloudrifts Phase 1 targets a fresh Ubuntu 24.04 x86_64 VPS with systemd. Start on a test VPS until you have reviewed the security notes and validated the flows you need.

## One-Line Install

Gist installer:

```bash
curl -fsSL https://gist.githubusercontent.com/RetrixDev/86efd4eab68602c54a3e3759ba24fec7/raw/cloudrifts-install.sh | sudo bash
```

Repository mirror:

```bash
curl -fsSL https://raw.githubusercontent.com/RetrixDev/cloudrifts/main/install.sh | sudo bash
```

The bootstrap script downloads the pinned version-tagged bundle, verifies SHA256, extracts the bundle, and runs the real installer.

## Requirements

- Ubuntu 24.04 LTS.
- x86_64/amd64 server.
- Root or sudo access.
- A normal VPS with systemd.
- Public IPv4 if you want browser setup, DNS checks, and Let's Encrypt SSL to work cleanly.
- Ports `8080`, `80`, and `443` reachable during setup. Managed DNS and mail roles need additional ports.

## What Gets Installed

The bundle installer installs Cloudrifts and the base server roles:

- Cloudrifts API daemon and PWA.
- Privileged Cloudrifts agent.
- Nginx, Apache, PHP-FPM, PHP extensions, MariaDB, PowerDNS packages, Postfix/Dovecot packages, Certbot, WP-CLI, and backup tooling.
- Systemd service `cloudriftsd`.
- A narrow sudoers policy for the `cloudrifts` system user to call `cloudrifts-agent` commands.

Default paths:

| Purpose | Path |
| --- | --- |
| Data | `/var/lib/cloudrifts` |
| Config | `/etc/cloudrifts` |
| Logs | `/var/log/cloudrifts` |
| Web UI | `/usr/local/share/cloudrifts/web` |
| Installer log | `/var/log/cloudrifts/install-*.log` |

## First Browser Setup

After install, Cloudrifts prints a one-time setup URL:

```text
http://YOUR_SERVER_IP:8080/setup/sso/...
```

Open that URL once. During setup you choose your own panel hostname, such as:

```text
panel.example.com
```

Cloudrifts suggests related hosts such as `webmail.example.com`, `phpmyadmin.example.com`, `ns1.example.com`, and `ns2.example.com`, but you can edit them.

## DNS Choices

Cloudrifts supports two setup modes:

- External DNS: keep DNS at your registrar, Cloudflare, Route 53, or another provider. Cloudrifts shows the records needed for websites and SSL.
- Managed DNS: set up PowerDNS on the same VPS and point registrar glue/delegation to your Cloudrifts nameservers.

For managed DNS, create glue records at your registrar:

| Host | Value |
| --- | --- |
| `ns1.example.com` | `YOUR_SERVER_IP` |
| `ns2.example.com` | `YOUR_SERVER_IP` |

Then delegate the domain to those nameservers.

## Manual Bundle Install

If you prefer to download the bundle yourself:

```bash
curl -fLO https://raw.githubusercontent.com/RetrixDev/cloudrifts/v0.1.0-dev/downloads/cloudrifts-panel-0.1.0-dev-linux-amd64.tar.gz
echo "41d2b1c7a6d345ba23254a81c00c9d856ca44bb089bf458f9ff31341ca74cd4a  cloudrifts-panel-0.1.0-dev-linux-amd64.tar.gz" | sha256sum -c -
tar -xzf cloudrifts-panel-0.1.0-dev-linux-amd64.tar.gz
cd cloudrifts-panel-0.1.0-dev-linux-amd64
sudo ./install.sh
```

## Useful Commands

```bash
systemctl status cloudriftsd
journalctl -u cloudriftsd -n 120 --no-pager
cloudriftsctl status --addr http://127.0.0.1:8080
cat /var/lib/cloudrifts/last-setup-url.txt
```

## Uninstall

From the extracted bundle:

```bash
sudo ./uninstall.sh
```

Remove Cloudrifts data as well:

```bash
sudo ./uninstall.sh --purge
```

## Troubleshooting

- If setup URL does not open, check the VPS firewall, cloud security group, and `systemctl status cloudriftsd`.
- If SSL fails, check that the website DNS record points to the VPS IPv4 and retry SSL from the website diagnostics panel.
- If managed DNS is enabled, verify registrar glue records before expecting public DNS to resolve.
- If the installer fails, inspect the path printed as `Install:` or run `tail -n 120 /var/log/cloudrifts/install-*.log`.

