package embedspawn

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

func maybeSetDeathSig(attr *os.ProcAttr) {
	// macos doesn't support Pdeathsig
}

// The macho library doesn't provide this!
//
// I based this struct on the others like macho.DylibCmd and the output of
// objtool -l which printed:
//
//	Load command 9
//	     cmd LC_LOAD_DYLINKER
//	 cmdsize 32
//	    name /usr/lib/dyld (offset 12)
type dylinkerCmd struct {
	Cmd  macho.LoadCmd
	Len  uint32
	Name uint32
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

	// Read the path to the ld shared library which is also needed?
	for _, load := range bin.Loads {
		loadBytes := load.Raw()
		if len(loadBytes) < 4 {
			continue
		}
		// https://cs.opensource.google/go/go/+/master:src/debug/macho/file.go;l=277;drc=e8fbad5de87f34d2e7632f94cac418c7436174ce
		cmd := macho.LoadCmd(bin.ByteOrder.Uint32(loadBytes[0:4]))
		if cmd != 0xe /*LC_LOAD_DYLINKER*/ { // pulled this from mach-o/loader.h
			continue
		}
		// Based the following code on:
		// https://cs.opensource.google/go/go/+/master:src/debug/macho/file.go;l=306,313;drc=e8fbad5de87f34d2e7632f94cac418c7436174ce
		var hdr dylinkerCmd
		if err := binary.Read(bytes.NewReader(loadBytes), bin.ByteOrder, &hdr); err != nil {
			return err
		}
		cstrName := loadBytes[hdr.Name:]
		i := bytes.IndexByte(cstrName, 0)
		if i == -1 {
			i = len(cstrName)
		}
		dylinker := string(cstrName[0:i])
		interp[dylinker] = struct{}{}
		break
	}

	// Read the libraries used by the binary (loaded by interp)
	imports, err := bin.ImportedLibraries()
	if err != nil {
		return err
	}

	for _, lib := range imports {
		if _, ok := libs[lib]; ok {
			continue
		}
		err := requiredLibsImpl(paths, lib, libs, interp)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("on macOS Big Sur (v11.0.1) and above chroots are impossible (without turning off SIP) because the system libraries are 'protected' and can't be copied in. Error message: %w", err)
		}
		if err != nil {
			return err
		}
		libs[lib] = struct{}{}
	}
	return nil
}
