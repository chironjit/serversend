.PHONY: check check-shared check-server check-desktop

# Component checks own their tooling and run without changing dependencies.
check: check-shared check-server check-desktop

check-shared:
	bash shared/scripts/check.sh

check-server:
	bash apps/server/scripts/check.sh

check-desktop:
	bash apps/desktop/scripts/check_test.sh
	bash apps/desktop/scripts/check.sh
