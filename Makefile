.PHONY: check check-shared check-server check-admin check-desktop check-layout check-bindings check-smoke check-persistence

# Component checks own their tooling and run without changing dependencies.
check: check-layout check-shared check-server check-desktop check-bindings check-smoke

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
