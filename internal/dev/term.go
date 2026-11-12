package dev

import (
	"os"
	"syscall"
	"unsafe"
)

// rawMode switches the terminal to unbuffered, unechoed input and returns the function that restores it.
func rawMode(fd int) (func(), error) {
	var saved syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCGETS, uintptr(unsafe.Pointer(&saved))); errno != 0 {
		return nil, errno
	}
	raw := saved
	raw.Lflag &^= syscall.ICANON | syscall.ECHO | syscall.ISIG | syscall.IEXTEN
	raw.Iflag &^= syscall.IXON | syscall.ICRNL
	raw.Cc[syscall.VMIN], raw.Cc[syscall.VTIME] = 1, 0
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return nil, errno
	}
	return func() {
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(&saved)))
	}, nil
}

func termSize(fd int) (int, int) {
	var size struct{ Rows, Cols, X, Y uint16 }
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 || size.Cols == 0 {
		return 100, 30
	}
	return int(size.Cols), int(size.Rows)
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// parseKeys splits raw terminal input into key names: letters as themselves, the rest by name.
func parseKeys(raw []byte) []string {
	var out []string
	for index := 0; index < len(raw); index++ {
		switch value := raw[index]; {
		case value == 0x1b && index+1 < len(raw) && raw[index+1] == '[':
			end := index + 2
			for end < len(raw) && (raw[end] < 0x40 || raw[end] > 0x7e) {
				end++
			}
			if end >= len(raw) {
				return out
			}
			name := map[string]string{"A": "up", "B": "down", "C": "right", "D": "left", "H": "home", "F": "end",
				"5~": "pgup", "6~": "pgdn", "1~": "home", "4~": "end", "Z": "backtab"}[string(raw[index+2:end+1])]
			if name != "" {
				out = append(out, name)
			}
			index = end
		case value == 0x1b:
			out = append(out, "esc")
		case value == '\r' || value == '\n':
			out = append(out, "enter")
		case value == '\t':
			out = append(out, "tab")
		case value == 3:
			out = append(out, "ctrl-c")
		case value == 0x7f || value == 8:
			out = append(out, "back")
		case value >= 0x20 && value < 0x7f:
			out = append(out, string(value))
		}
	}
	return out
}
