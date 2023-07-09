//go:build linux || freebsd || openbsd || netbsd || dragonfly

package embedspawn

import (
	"debug/elf"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

func maybeSetDeathSig(attr *os.ProcAttr) {
	attr.Sys.Pdeathsig = syscall.SIGHUP
}

func requiredLibsImpl(paths []string, filename string, libs map[string]struct{}, interp map[string]struct{}) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	bin, err := elf.NewFile(f)
	if err != nil {
		return err
	}

	// Read the path to the ld shared library which is also needed
	for _, prog := range bin.Progs {
		if prog.Type != elf.PT_INTERP {
			continue
		}
		interpData := make([]byte, prog.Filesz-1) // -1 to cut off the \0 on the end
		_, err := prog.ReadAt(interpData, 0)
		if err != nil {
			return fmt.Errorf("Failed to read interp data from elf: %w", err)
		}
		interp[string(interpData)] = struct{}{}
		break
	}

	// Read the libraries used by the binary (loaded by the interp)
	imports, err := bin.ImportedLibraries()
	if err != nil {
		return err
	}

	for _, lib := range imports {
		for _, path := range paths {
			libPath := filepath.Join(path, lib)
			if _, ok := libs[libPath]; ok {
				continue
			}
			err := requiredLibsImpl(paths, libPath, libs, interp)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			libs[libPath] = struct{}{}
		}
	}
	return nil
}

func limitGroupsForMac(groups []uint32) []uint32 {
	return groups
}
