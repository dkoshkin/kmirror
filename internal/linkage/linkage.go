// Copyright 2026 Dimitri Koshkin. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package linkage checks that a binary can be hooked by mirrord.
package linkage

import (
	"debug/elf"
	"fmt"
	"runtime"
)

// Check returns an error if mirrord can't hook the binary at path.
//
// On Linux mirrord injects with LD_PRELOAD, which only the dynamic loader honors, so a statically linked binary
// would silently run with the local network, files and environment. macOS binaries always link libSystem
// dynamically, so there is nothing to check there.
func Check(path string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	return checkELF(path)
}

func checkELF(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("%s is not a Linux ELF binary: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return nil
		}
	}
	return fmt.Errorf("%s is statically linked, so mirrord can't hook it; "+
		"rebuild it with CGO_ENABLED=1 (and without -tags netgo or -extldflags -static)", path)
}
