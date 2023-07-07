package embedspawn

import (
	"debug/macho"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func maybeSetDeathSig(attr *os.ProcAttr) {
	// macos doesn't support Pdeathsig
}

func requiredLibsImpl(paths []string, filename string, libs map[string]struct{}, interp map[string]struct{}) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	bin, err := macho.NewFile(f)
	if err != nil {
		return err
	}

	// TODO: Read the path to the ld shared library which is also needed?

	// Read the libraries used by the binary (loaded by interp)
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
