# Security Notes

Cloudrifts controls real server services and files. Treat the current release as an early public test build and install it first on a fresh VPS that you can rebuild.

## Current Security Model

- The browser panel talks to `cloudriftsd`.
- Privileged host operations go through `cloudrifts-agent`.
- The installer creates a `cloudrifts` system user.
- The installer writes a narrow sudoers policy for specific `cloudrifts-agent` command prefixes.
- First setup uses a one-time setup URL generated on the server.

## Recommended Use

- Use a fresh Ubuntu 24.04 VPS.
- Keep the one-time setup URL private.
- Prefer testing behind your own firewall or trusted IP allowlist.
- Review open ports before exposing the panel widely.
- Put the panel behind HTTPS for real use.
- Keep snapshots/backups before testing restore flows.

## Network Ports

The installer opens ports through UFW only when UFW is active. Typical ports:

| Purpose | Port |
| --- | --- |
| Temporary panel setup/API | `8080/tcp` |
| HTTP | `80/tcp` |
| HTTPS | `443/tcp` |
| Managed DNS | `53/tcp`, `53/udp` |
| Mail role | `25/tcp`, `465/tcp`, `587/tcp`, `143/tcp`, `993/tcp` |

## Report Issues

Open a GitHub issue with:

- Cloudrifts version.
- Ubuntu version.
- The command you ran.
- The relevant job log or installer log.
- Any sensitive values redacted.

