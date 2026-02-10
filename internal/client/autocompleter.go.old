package client

import (
	"strings"
)

// Completer implements readline.Completer interface
type Completer struct {
	handler *CommandHandler
}

func (c *Completer) Do(line []rune, pos int) (newLine [][]rune, length int) {
	lineStr := string(line[:pos])
	parts := strings.Fields(lineStr)

	if len(parts) == 0 {
		return nil, 0
	}

	command := parts[0]
	
	// If it's just the command being typed, we can suggest commands
	if len(parts) == 1 && !strings.HasSuffix(lineStr, " ") {
		commands := []string{"ls", "pwd", "cd", "create", "mkdir", "upload", "download", "read", "write", "info", "help", "clear", "exit"}
		var suggestions [][]rune
		for _, cmd := range commands {
			if strings.HasPrefix(cmd, command) {
				suggestions = append(suggestions, []rune(cmd[len(command):]))
			}
		}
		return suggestions, len(command)
	}

	// For commands that take a filename/path, fetch from server
	needsFile := map[string]bool{
		"cd":       true,
		"read":     true,
		"write":    true,
		"download": true,
	}

	if needsFile[command] {
		prefix := ""
		if len(parts) > 1 {
			prefix = parts[1]
		}
		if strings.HasSuffix(lineStr, " ") && len(parts) == 1 {
			prefix = ""
		}

		files, err := c.handler.client.ListFiles()
		if err != nil {
			return nil, 0
		}

		var suggestions [][]rune
		for _, f := range files {
			if strings.HasPrefix(f.Name, prefix) {
				suggestions = append(suggestions, []rune(f.Name[len(prefix):]))
			}
		}
		return suggestions, len(prefix)
	}

	return nil, 0
}