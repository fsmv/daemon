//go:build !windows

package embedportal

import "os"

func atomicReplaceFile(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}
