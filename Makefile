.PHONY: release changelog

# make release [BUMP=auto|patch|minor|major|X.Y.Z]: release origin/main from GitHub Actions.
BUMP ?= auto
release:
	@scripts/release.sh $(BUMP)

# Preview the unreleased changelog section (needs git-cliff).
changelog:
	@git cliff --unreleased --bump --strip header
