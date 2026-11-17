.PHONY: local-test-server-setup local-test-desktop local-test-reset check check-shared check-server check-admin check-desktop check-layout check-bindings check-smoke check-persistence

.PHONY: release check-release
export VERSION DRY_RUN

release:
	node scripts/release.mjs

check-release:
	node --test scripts/release.test.mjs

# Component checks own their tooling and run without changing dependencies.
check: check-release check-layout check-shared check-server check-desktop check-bindings check-smoke

check-shared:
	bash shared/scripts/check.sh

check-server:
	bash apps/server/scripts/check.sh

check-admin:
	bash apps/server/scripts/check_admin.sh

check-desktop:
	bash apps/desktop/scripts/check_test.sh
	bash apps/desktop/scripts/check.sh

check-layout:
	node apps/desktop/scripts/check_layout.mjs

check-bindings:
	bash apps/desktop/scripts/check_bindings.sh

check-smoke:
	bash apps/desktop/scripts/check_smoke.sh

check-persistence:
	bash shared/scripts/check_persistence.sh

# Guided local end-to-end testing; see docs/local-testing.md.
local-test-server-setup:
	@bash apps/server/scripts/local_test_server.sh

local-test-desktop:
	@bash apps/desktop/scripts/local_test_desktop.sh

local-test-reset:
	@bash apps/server/scripts/local_test_server.sh --reset
