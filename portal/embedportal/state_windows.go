package embedportal

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// TODO: maybe try to call ReplaceFile, but it's not in the package
// https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-replacefilea
// https://learn.microsoft.com/en-us/windows/win32/fileio/deprecation-of-txf?redirectedfrom=MSDN#applications-updating-a-single-file-with-document-like-data
func atomicReplaceFile(oldpath, newpath string) error {
	old16, err := windows.UTF16PtrFromString(oldpath)
	if err != nil {
		return fmt.Errorf("Failed to convert oldpath %q into a windows string: %w",
			oldpath, err)
	}
	new16, err := windows.UTF16PtrFromString(newpath)
	if err != nil {
		return fmt.Errorf("Failed to convert newpath %q into a windows string: %w",
			newpath, err)
	}
	// os.Rename doesn't include WRITE_THROUGH
	return windows.MoveFileEx(old16, new16,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
