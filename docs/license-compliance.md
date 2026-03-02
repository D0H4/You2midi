# Third-Party License Compliance

## Scope
This check focuses on **bundled runtime binaries/models** shipped with the desktop installer.

Tracked inventory file:
- `docs/third_party_license_inventory.json`

Artifact source:
- `dist/desktop/artifact-manifest.json`

## Verification Rule
For every tracked component that is physically present in the artifact manifest:
- `status` must be `approved`
- `license_expression` must not be empty or `TBD`

If either condition fails, release packaging must fail.

## Run Locally
1. Generate artifact manifest:
   - `make manifest`
2. Verify license compliance:
   - `powershell -ExecutionPolicy Bypass -File scripts/verify_license_compliance.ps1 -Strict`

## Status Values
- `approved`: verified and allowed to distribute.
- `review_required`: not yet cleared for redistribution.
- `prohibited`: explicitly blocked from redistribution.

## Release Process
1. When adding a new bundled runtime/model under `dist/desktop/runtime/**`, add/update an inventory entry.
2. Set `status=approved` only after upstream license terms and redistribution obligations are confirmed.
3. Include required third-party notices in the installer package.
