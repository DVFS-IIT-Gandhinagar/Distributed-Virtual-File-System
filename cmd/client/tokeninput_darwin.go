//go:build darwin

package main

import (
	"bufio"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// readTokenLine reads one line of input from the terminal.
//
// macOS caps a line in canonical (cooked) tty mode at MAX_CANON = 1024 bytes and silently
// discards everything past it, including the newline from Enter. Google ID tokens are longer
// than that, so a pasted token could never be submitted. We switch the terminal to
// non-canonical mode for the duration of the read and handle echo/backspace ourselves.
func readTokenLine(reader *bufio.Reader) (string, error) {
	fd := int(os.Stdin.Fd())
	oldState, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil || reader.Buffered() > 0 {
		// Not a terminal (e.g. piped input) or input already buffered: no line limit applies.
		return reader.ReadString('\n')
	}

	newState := *oldState
	newState.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG
	newState.Cc[unix.VMIN] = 1
	newState.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &newState); err != nil {
		return reader.ReadString('\n')
	}
	defer unix.IoctlSetTermios(fd, unix.TIOCSETA, oldState)

	var line []byte
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return string(line), err
		}
		switch {
		case b == '\r' || b == '\n':
			os.Stdout.WriteString("\n")
			return string(line), nil
		case b == 0x7f || b == 0x08: // Backspace / Delete
			if len(line) > 0 {
				line = line[:len(line)-1]
				os.Stdout.WriteString("\b \b")
			}
		case b == 0x03: // Ctrl+C
			os.Stdout.WriteString("^C\n")
			return "", errors.New("interrupted")
		case b == 0x04: // Ctrl+D
			if len(line) == 0 {
				return "", io.EOF
			}
		case b < 0x20: // Ignore other control characters
		default:
			line = append(line, b)
			os.Stdout.Write([]byte{b})
		}
	}
}
