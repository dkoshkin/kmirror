# Copyright 2026 Dimitri Koshkin. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

.PHONY: release-please
release-please:
# filter Returns all whitespace-separated words in text that do match any of the pattern words.
ifeq ($(filter main release/v%,$(GIT_CURRENT_BRANCH)),)
	$(error "release-please should only be run on the main or release branch")
else
	release-please release-pr --repo-url $(GITHUB_ORG)/$(GITHUB_REPOSITORY) --target-branch $(GIT_CURRENT_BRANCH) --token "$$(gh auth token)"
endif
