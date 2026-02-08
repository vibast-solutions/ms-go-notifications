E2E Tests (Docker Compose)

Prereqs:
- Docker Desktop (or Docker Engine) running.

Run:
1. cd notifications/e2e
2. docker compose up -d --build
3. cd ..
4. go test ./e2e -v -tags e2e

Shortcut:
- notifications/e2e/run.sh

Teardown:
- cd notifications/e2e
- docker compose down -v
