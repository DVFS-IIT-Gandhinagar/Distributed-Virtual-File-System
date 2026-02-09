package client

import (
	"fmt"
	"io"
	"strings"

	"github.com/chzyer/readline"
	"github.com/umangshikarvar/dvfs/internal/domain"
)

// CommandHandler handles terminal-based commands for the client
type CommandHandler struct {
	client   *Client
	instance *readline.Instance
}

// NewCommandHandler creates a new command handler
func NewCommandHandler(client *Client) *CommandHandler {
	h := &CommandHandler{
		client: client,
	}
	return h
}

// Start begins the interactive command loop
func (h *CommandHandler) Start() {
	completer := &Completer{handler: h}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "\033[32mdvfs>\033[0m ",
		HistoryFile:     "/tmp/readline.tmp",
		AutoComplete:    completer,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		fmt.Printf("Error initializing readline: %v\n", err)
		return
	}
	defer rl.Close()
	h.instance = rl

	fmt.Println("=== Distributed VFS Client ===")
	fmt.Println("Available commands: ls, pwd, cd, create, mkdir, upload, download, read, write, info, help, exit")
	fmt.Println()

	for {
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				if len(line) == 0 {
					break
				} else {
					continue
				}
			} else if err == io.EOF {
				break
			}
			fmt.Printf("Error reading input: %v\n", err)
			continue
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		command := parts[0]

		switch command {
		case "ls", "list":
			h.handleList()
		case "pwd":
			h.handlePath()
		case "cd":
			if len(parts) < 2 {
				fmt.Println("Usage: cd <relative_path>")
				continue
			}
			h.handleChangeDir(parts[1])
		case "create":
			if len(parts) < 2 {
				fmt.Println("Usage: create <filename>")
				continue
			}
			h.handleCreateFile(parts[1])
		case "read":
			if len(parts) < 2 {
				fmt.Println("Usage: read <filename>")
				continue
			}
			h.handleReadFile(parts[1])
		case "write":
			if len(parts) < 2 {
				fmt.Println("Usage: write <filename> <data>")
				continue
			}
			h.handleWriteFile(parts[1], strings.Join(parts[2:], " "))
		case "mkdir":
			if len(parts) < 2 {
				fmt.Println("Usage: mkdir <dirname>")
				continue
			}
			h.handleCreateDir(parts[1])
		case "upload":
			if len(parts) < 2 {
				fmt.Println("Usage: upload <local_path>")
				continue
			}
			h.handleUploadFile(parts[1])
		case "download":
			if len(parts) < 2 {
				fmt.Println("Usage: download <filename>")
				continue
			}
			h.handleDownloadFile(parts[1])
		case "info":
			h.handleInfo()
		case "clear":
			h.handleClear()
		case "help":
			h.handleHelp()
		case "exit", "quit":
			fmt.Println("Goodbye!")
			return
		default:
			fmt.Printf("Unknown command: %s\n", command)
			fmt.Println("Type 'help' for available commands")
		}
	}
}

// handlePath returns the current path
func (h *CommandHandler) handlePath() {
	path, err := h.client.Path()
	if err != nil {
		fmt.Printf("Error getting the pwd: %v\n", err)
		return
	}

	fmt.Println(path)
}

// handleChangeDir changes current Dir
func (h *CommandHandler) handleChangeDir(relative_path string) {
	err := h.client.ChangeDirectory(relative_path)
	if err != nil {
		fmt.Printf("Error changing the current directory: %v\n", err)
		return
	}
}

// handleUploadFile uploads the file to current directory
func (h *CommandHandler) handleUploadFile(path string) {
	err := h.client.UploadFile(path)
	if err != nil {
		fmt.Printf("Error uploading the file: %v\n", err)
		return
	}
}

// handleList lists files in the current directory
func (h *CommandHandler) handleList() {
	files, err := h.client.ListFiles()
	if err != nil {
		fmt.Printf("Error uploading the file: %v\n", err)
		return
	}

	if len(files) == 0 {
		fmt.Println("(empty directory)")
		return
	}

	fmt.Printf("%-20s %-10s %10s\n", "Name", "Type", "Size")
	fmt.Printf("%-20s %-10s %10s\n", "----", "----", "----")

	for _, file := range files {
		typeStr := "file"
		if file.Type == domain.InodeTypeDirectory {
			typeStr = "dir"
		}
		fmt.Printf("%-20s %-10s %10d\n", file.Name, typeStr, file.Size)
	}
}

// handleCreateFile creates a new file
func (h *CommandHandler) handleCreateFile(filename string) {
	if filename == "" {
		fmt.Println("Error: filename cannot be empty")
		return
	}

	// check for nested paths
	if strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		fmt.Println("Error: nested paths are not supported")
		return
	}

	file, err := h.client.CreateFile(filename)
	if err != nil {
		fmt.Printf("Error creating file '%s': %v\n", filename, err)
		return
	}

	fmt.Printf("File '%s' created successfully (FID: %s)\n", file.Name, file.FID.String())
}

// handleReadFile reads a file
func (h *CommandHandler) handleReadFile(filename string) {
	if filename == "" {
		fmt.Println("Error: filename cannot be empty")
		return
	}
	// read full file for now
	data, err := h.client.ReadFile(filename)
	if err != nil {
		fmt.Printf("Error reading file '%s': %v\n", filename, err)
		return
	}
	fmt.Printf("Contents of '%s':\n%s\n", filename, string(data))
}

// handleDownloadFile reads a file
func (h *CommandHandler) handleDownloadFile(filename string) {
	if filename == "" {
		fmt.Println("Error: filename cannot be empty")
		return
	}
	// read full file for now
	err := h.client.DownloadFile(filename)
	if err != nil {
		fmt.Printf("Error downloading file '%s': %v\n", filename, err)
		return
	}
	fmt.Printf("File downloaded successfully\n")
}

func (h *CommandHandler) handleWriteFile(filename, data string) {
	if filename == "" {
		fmt.Println("Error: filename cannot be empty")
		return
	}
	err := h.client.WriteFile(filename, []byte(data))
	if err != nil {
		fmt.Printf("Error writing to file '%s': %v\n", filename, err)
		return
	}
	fmt.Printf("Successfully wrote to file '%s'\n", filename)
}

// handleCreateDir creates a new directory
func (h *CommandHandler) handleCreateDir(dirname string) {
	if dirname == "" {
		fmt.Println("Error: directory name cannot be empty")
		return
	}

	// check for nested paths
	if strings.Contains(dirname, "/") || strings.Contains(dirname, "\\") {
		fmt.Println("Error: nested paths are not supported")
		return
	}

	dir, err := h.client.CreateDirectory(dirname)
	if err != nil {
		fmt.Printf("Error creating directory '%s': %v\n", dirname, err)
		return
	}

	fmt.Printf("Directory '%s' created successfully (FID: %s)\n", dir.Name, dir.FID.String())
}

// handleInfo shows information about the root directory
func (h *CommandHandler) handleInfo() {
	info, err := h.client.GetFileInfo()
	if err != nil {
		fmt.Printf("Error getting file info: %v\n", err)
		return
	}

	fmt.Printf("Directory Information:\n")
	fmt.Printf("  Name: %s\n", info.Name)
	fmt.Printf("  Type: %s\n", info.Type)
	fmt.Printf("  Size: %d bytes\n", info.Size)
	fmt.Printf("  FID:  %s\n", info.FID.String())
}

// handleClear clears the terminal screen
func (h *CommandHandler) handleClear() {
	fmt.Print("\033[H\033[2J")
}

// handleHelp displays available commands
func (h *CommandHandler) handleHelp() {
	fmt.Println("Available commands:")
	fmt.Println("  ls, list            - List files and directories")
	fmt.Println("  pwd                 - Returns parent working directory")
	fmt.Println("  cd <relative_path>  - Change current directory")
	fmt.Println("  upload <local_path> - Upload a new file the current directory")
	fmt.Println("  download <filename> - Download the file from current directory")
	fmt.Println("  create <filename>   - Create a new file")
	fmt.Println("  mkdir <dirname>     - Create a new directory")
	fmt.Println("  read <filename>     - Read the file from current directory")
	fmt.Println("  write <filename> <data> - Write data to a file in the current directory")
	fmt.Println("  info                - Show root directory information")
	fmt.Println("  clear               - Clear the terminal screen")
	fmt.Println("  help                - Show this help message")
	fmt.Println("  exit, quit          - Exit the client")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  create hello.txt")
	fmt.Println("  mkdir documents")
	fmt.Println("  ls")
}
