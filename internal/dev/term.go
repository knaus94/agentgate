package dev

import (
	"bytes"
	"os"
	"strings"
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
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 || size.Cols == 0 || size.Rows == 0 {
		return 100, 30
	}
	return int(size.Cols), int(size.Rows)
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	var state syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&state)))
	return errno == 0
}

// terminalInput uses a bounded poll so closing the panel also ends its reader.
// Closing a UI must not leave a goroutine blocked forever on the shared stdin.
func terminalInput(file *os.File, done <-chan struct{}) <-chan []byte {
	keys := make(chan []byte, 8)
	go func() {
		defer close(keys)
		fd := int(file.Fd())
		for {
			select {
			case <-done:
				return
			default:
			}
			var readable syscall.FdSet
			if fd < 0 || fd/64 >= len(readable.Bits) {
				return
			}
			readable.Bits[fd/64] |= 1 << uint(fd%64)
			timeout := syscall.Timeval{Usec: 100000}
			count, err := syscall.Select(fd+1, &readable, nil, nil, &timeout)
			if err == syscall.EINTR {
				continue
			}
			if err != nil {
				return
			}
			if count == 0 {
				continue
			}
			buffer := make([]byte, 256)
			count, err = syscall.Read(fd, buffer)
			if err == syscall.EINTR || err == syscall.EAGAIN {
				continue
			}
			if err != nil || count == 0 {
				return
			}
			select {
			case keys <- buffer[:count]:
			case <-done:
				return
			}
		}
	}()
	return keys
}

// parseKeys splits raw terminal input into key names: letters as themselves, the rest by name.
func parseKeys(raw []byte) []string {
	decoder := keyDecoder{}
	return append(decoder.feed(raw), decoder.flush()...)
}

// keyDecoder retains incomplete escape sequences across reads. Pipes and PTYs
// do not guarantee one complete key sequence per Read. Bracketed paste is
// ignored so pasted task text cannot trigger navigation or quit shortcuts.
type keyDecoder struct {
	pending []byte
	paste   bool
}

func (d *keyDecoder) feed(raw []byte) []string {
	d.pending = append(d.pending, raw...)
	var out []string
	for len(d.pending) > 0 {
		if d.paste {
			end := bytes.Index(d.pending, []byte("\x1b[201~"))
			if end < 0 {
				if len(d.pending) > 5 {
					d.pending = d.pending[len(d.pending)-5:]
				}
				break
			}
			d.pending, d.paste = d.pending[end+6:], false
			continue
		}
		raw = d.pending
		consumed := 1
		switch value := raw[0]; {
		case value == 0x1b && len(raw) == 1:
			return out
		case value == 0x1b && (raw[1] == '[' || raw[1] == 'O'):
			end := 2
			for end < len(raw) && (raw[end] < 0x40 || raw[end] > 0x7e) {
				end++
			}
			if end >= len(raw) {
				if len(raw) > 32 {
					d.pending = nil
				}
				return out
			}
			sequence := string(raw[2 : end+1])
			if sequence == "200~" {
				d.paste = true
			}
			if strings.Contains(sequence, ";") && raw[end] >= 'A' && raw[end] <= 'Z' {
				sequence = string(raw[end])
			}
			name := map[string]string{"A": "up", "B": "down", "C": "right", "D": "left", "H": "home", "F": "end",
				"5~": "pgup", "6~": "pgdn", "1~": "home", "4~": "end", "7~": "home", "8~": "end", "Z": "backtab"}[sequence]
			if name != "" {
				out = append(out, name)
			}
			consumed = end + 1
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
		d.pending = d.pending[consumed:]
	}
	return out
}

func (d *keyDecoder) flush() []string {
	if d.paste {
		return nil
	}
	if len(d.pending) == 1 && d.pending[0] == 0x1b {
		d.pending = nil
		return []string{"esc"}
	}
	d.pending = nil
	return nil
}
