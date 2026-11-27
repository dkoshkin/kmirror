// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package linkage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckELFRejectsNonELF(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkELF(path); err == nil {
		t.Error("checkELF() of a shell script should fail")
	}
}
