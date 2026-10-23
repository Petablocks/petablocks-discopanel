# 📝 PETABLOCKS DiscoPanel Fork — Changelog

All notable PETABLOCKS customizations and upstream synchronizations for `petablocks-discopanel` are documented in this file.

---

## [1.1.0] - 2026-10-23
### Added
- **🛡️ Isolated PETABLOCKS Extension Package (`internal/petablocks/`)**:
  - Designed a conflict-free extension architecture so upstream `nickheyer/discopanel` updates can be merged cleanly.
- **🔑 Machine-to-Machine Internal Token Auth (`internal/petablocks/auth.go`)**:
  - Added constant-time Bearer token authentication supporting `DISCOPANEL_INTERNAL_API_TOKEN` and `API_SECRET_TOKEN` (with `API_SECRET_TOKEN_PREVIOUS` rotation window) for headless ConnectRPC communication from `petablocks-admin`.
- **🔍 Non-Destructive Container Auto-Discovery & Adoption (`internal/petablocks/adopt.go`)**:
  - Automatically scans and adopts existing Minecraft & Velocity containers on startup and via `POST /api/v1/petablocks/adopt` without stopping or wiping worlds.
  - Added first-class recognition for `petablocks-modpack-s2-test` (Modpack Season 2 Test Server), `pb-paper-smp` (Paper 26.3 Duo SMP), `petablocks-create-2`, `petablocks-patreon-creative`, `pb-lobby-main`, `pb-velocity-proxy`, and `petablocks-modpack-main`.
- **⚡ Autonomous Telemetry Companion Config Provisioner (`internal/petablocks/telemetry.go`)**:
  - Subscribed to DiscoPanel's `events.Bus` (`SERVER_START`, `SERVER_RESTART`) to automatically verify and provision `config/petablocks-telemetry.json` (or `plugins/PetablocksTelemetry/config.json`) pointing to the internal PETABLOCKS WebSocket bridge (`ws://10.20.110.116:3000/ws/servers/bridge`).

### Infrastructure
- **📦 Automated GHCR Container Build (`.github/workflows/build.yml`)**:
  - Added GitHub Actions CI/CD pipeline to build and publish `ghcr.io/petablocks/petablocks-discopanel:latest`.
