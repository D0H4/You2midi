.PHONY: build test test-integration lint vet generate-api perf-gate desktop-build updater-build launcher-build patch-build runtime-manifest installer manifest smoke-postinstall license-check security-check support-bundle

# ---- build ------------------------------------------------------------------
build:
	go build -o bin/you2midi.exe ./

generate-api:
	go generate ./internal/api/http
	cd frontend && npm run generate:api

# ---- test -------------------------------------------------------------------
## Unit tests only (no external deps required — runs in CI)
test:
	go test ./... -timeout 60s -race

## Integration tests (requires transkun, neuralnote, yt-dlp installed)
test-integration:
	go test ./... -tags integration -timeout 300s

# ---- quality ----------------------------------------------------------------
vet:
	go vet ./...

lint:
	staticcheck ./...

# ---- dev --------------------------------------------------------------------
run:
	go run . -config config.toml

## Download golden fixture audio samples for regression tests (Issue #12)
golden:
	powershell -ExecutionPolicy Bypass -File scripts/download_golden.ps1

## Performance gate (latency/throughput/allocation thresholds)
perf-gate:
	powershell -ExecutionPolicy Bypass -File scripts/perf_gate.ps1

## Build Wails desktop executable into dist/desktop (requires initialized Wails project)
desktop-build:
	powershell -ExecutionPolicy Bypass -File scripts/desktop_build.ps1

## Build standalone updater executable into dist/desktop
updater-build:
	go build -o dist/desktop/you2midi-updater.exe ./cmd/updater

## Build standalone launcher executable into dist/desktop
launcher-build:
	go build -o dist/desktop/you2midi-launcher.exe ./cmd/launcher

## Build a lightweight patch ZIP from selected desktop artifacts
patch-build:
	powershell -ExecutionPolicy Bypass -File scripts/build_patch.ps1

## Write runtime/python-runtime.json (requires -ArchiveUrl argument when invoking script directly)
runtime-manifest:
	powershell -ExecutionPolicy Bypass -File scripts/set_python_runtime_manifest.ps1

## Build Inno Setup installer from dist/desktop
installer:
	powershell -ExecutionPolicy Bypass -File scripts/build_installer.ps1

## Generate SHA256 manifest for packaged desktop artifacts
manifest:
	powershell -ExecutionPolicy Bypass -File scripts/generate_artifact_manifest.ps1

## Verify third-party license compliance for bundled runtimes/models
license-check:
	powershell -ExecutionPolicy Bypass -File scripts/verify_license_compliance.ps1 -Strict

## Verify Windows signature/Defender checks and generate security report
security-check:
	powershell -ExecutionPolicy Bypass -File scripts/verify_windows_security.ps1

## Bundle diagnostics for support (config/manifests/security report/workspace DB)
support-bundle:
	powershell -ExecutionPolicy Bypass -File scripts/bundle_support_logs.ps1

## Smoke test packaged backend binary; pass -RunFullTranscription via script for full e2e check
smoke-postinstall:
	powershell -ExecutionPolicy Bypass -File scripts/smoke_post_install.ps1
