//go:build !darwin

package main

import "bufio"

// readTokenLine reads one line of input. Only macOS needs special handling for long lines
// (see tokeninput_darwin.go); elsewhere the canonical line buffer is large enough.
func readTokenLine(reader *bufio.Reader) (string, error) {
	return reader.ReadString('\n')
}
