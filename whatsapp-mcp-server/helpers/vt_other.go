//go:build !windows

package helpers

// enableVirtualTerminal is a no-op on non-Windows platforms, where terminals
// already interpret ANSI/VT escape sequences.
func enableVirtualTerminal() {}
