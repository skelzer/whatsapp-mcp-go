//go:build windows

package helpers

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVirtualTerminal turns on ANSI/VT escape-sequence processing for the
// console so the wizard can clear the screen and redraw the QR in place.
func enableVirtualTerminal() {
	const enableVirtualTerminalProcessing = 0x0004
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return
	}
	_ = windows.SetConsoleMode(h, mode|enableVirtualTerminalProcessing)
}
