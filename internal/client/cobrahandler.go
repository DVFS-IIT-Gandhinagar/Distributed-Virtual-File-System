package client

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chzyer/readline"
	"github.com/spf13/cobra"
	"github.com/umangshikarvar/dvfs/internal/domain"
)

// CobraHandler handles commands using Cobra
type CobraHandler struct {
	cacheHandler *CacheHandler
	rootCmd      *cobra.Command
	rl           *readline.Instance
}

// NewCobraHandler creates a new Cobra-based command handler
func NewCobraHandler(cacheHandler *CacheHandler) *CobraHandler {
	h := &CobraHandler{
		cacheHandler: cacheHandler,
	}
	h.setupCommands()
	return h
}

func (h *CobraHandler) setupCommands() {
	h.rootCmd = &cobra.Command{
		Use:           "dvfs",
		Short:         "Distributed Virtual File System Client",
		SilenceUsage:  true,
		SilenceErrors: true, // Don't print errors, we handle them manually
	}

	// ls
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "ls",
		Short: "List files and directories",
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := h.cacheHandler.ListFiles()
			if err != nil {
				return err
			}
			if len(files) == 0 {
				fmt.Println("(empty directory)")
				return nil
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
			return nil
		},
	})

	// refresh
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "refresh",
		Short: "Refresh current directory cache from server",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := h.cacheHandler.Refresh()
			if err == nil {
				fmt.Println("Current directory cache refreshed.")
			}
			return err
		},
	})

	// share
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "sharewith <username>",
		Short: "Share your root directory with another user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]
			fmt.Printf("Sharing root directory with '%s'...\n", username)
			err := h.cacheHandler.Share(username)
			if err == nil {
				fmt.Printf("Root directory shared successfully with '%s'\n", username)
			}
			return err
		},
	})

	// unshare
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "unsharewith <username>",
		Short: "Share your root directory with another user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]
			fmt.Printf("Unsharing root directory with '%s'...\n", username)
			err := h.cacheHandler.Unshare(username)
			if err == nil {
				fmt.Printf("Root directory unshared successfully with '%s'\n", username)
			}
			return err
		},
	})

	// pwd
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "pwd",
		Short: "Returns parent working directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := h.cacheHandler.Path()
			if err != nil {
				return err
			}
			fmt.Println(path)
			return nil
		},
	})

	// cd
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "cd <relative_path>",
		Short: "Change current directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return h.cacheHandler.ChangeDirectory(args[0])
		},
	})

	// upload
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "upload <local_path>",
		Short: "Upload a file or folder to the current directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Uploading '%s'...\n", args[0])
			err := h.cacheHandler.Upload(args[0])
			if err == nil {
				fmt.Printf("'%s' uploaded successfully\n", args[0])
			}
			return err
		},
	})

	// download
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "download <name>",
		Short: "Download a file or folder from the current directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Downloading '%s'...\n", args[0])
			err := h.cacheHandler.Download(args[0])
			if err == nil {
				fmt.Printf("'%s' downloaded successfully\n", args[0])
			}
			return err
		},
	})

	// create
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "create <filename>",
		Short: "Create a new file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := h.cacheHandler.CreateFile(args[0])
			if err == nil {
				fmt.Printf("File '%s' created successfully (FID: %s)\n", file.Name, file.FID.String())
			}
			return err
		},
	})

	// mkdir
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "mkdir <dirname>",
		Short: "Create a new directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := h.cacheHandler.CreateDirectory(args[0])
			if err == nil {
				fmt.Printf("Directory '%s' created successfully (FID: %s)\n", dir.Name, dir.FID.String())
			}
			return err
		},
	})

	// read
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "read <filename>",
		Short: "Read a file from current directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := h.cacheHandler.ReadFile(args[0])
			if err == nil {
				fmt.Printf("Contents of '%s':\n%s\n", args[0], string(data))
			}
			return err
		},
	})

	// // write
	// h.rootCmd.AddCommand(&cobra.Command{
	// 	Use:   "write <filename> <data>",
	// 	Short: "Write data to a file in the current directory",
	// 	Args:  cobra.MinimumNArgs(2),
	// 	RunE: func(cmd *cobra.Command, args []string) error {
	// 		filename := args[0]
	// 		data := strings.Join(args[1:], " ")
	// 		err := h.cacheHandler.WriteFile(filename, []byte(data))
	// 		if err == nil {
	// 			fmt.Printf("Successfully wrote to file '%s'\n", filename)
	// 		}
	// 		return err
	// 	},
	// })

	// delete
	deleteCmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a file or directory",
		Long:  "Delete a file or empty directory. Use -r to delete non-empty directories recursively. Use -t to permanently delete an item from trash (recursive is implied).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fromTrash, _ := cmd.Flags().GetBool("trash")
			if fromTrash {
				err := h.cacheHandler.DeleteFromTrash(args[0])
				if err == nil {
					fmt.Printf("Permanently deleted '%s' from trash\n", args[0])
				}
				return err
			}

			recursive, _ := cmd.Flags().GetBool("recursive")
			fmt.Printf("[DEBUG] Deleting '%s' with recursive=%v\n", args[0], recursive)
			err := h.cacheHandler.DeleteFile(args[0], recursive)
			if err == nil {
				if recursive {
					fmt.Printf("Successfully deleted '%s' and all its contents\n", args[0])
				} else {
					fmt.Printf("Successfully deleted '%s'\n", args[0])
				}
			}
			return err
		},
	}
	deleteCmd.Flags().BoolP("recursive", "r", false, "Delete directories recursively")
	deleteCmd.Flags().BoolP("trash", "t", false, "Permanently delete an item from trash")
	h.rootCmd.AddCommand(deleteCmd)

	// trash (soft delete)
	trashCmd := &cobra.Command{
		Use:   "trash <name>",
		Short: "Move a file or directory to trash",
		Long:  "Soft delete: moves a file or directory into the user's .trash directory. Use -r for non-empty directories.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			recursive, _ := cmd.Flags().GetBool("recursive")
			trashedName, err := h.cacheHandler.TrashFile(args[0], recursive)
			if err == nil {
				if trashedName != args[0] {
					fmt.Printf("Moved '%s' to trash as '%s'\n", args[0], trashedName)
				} else {
					fmt.Printf("Moved '%s' to trash\n", args[0])
				}
			}
			return err
		},
	}
	trashCmd.Flags().BoolP("recursive", "r", false, "Trash directories recursively")
	h.rootCmd.AddCommand(trashCmd)

	// restore
	restoreCmd := &cobra.Command{
		Use:   "restore <name>",
		Short: "Restore a file or directory from trash",
		Long:  "Restores an item from .trash back to its original location (best-effort; requires server-side metadata).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			restoredName, err := h.cacheHandler.RestoreFile(args[0])
			if err == nil {
				if restoredName != args[0] {
					fmt.Printf("Restored '%s' as '%s'\n", args[0], restoredName)
				} else {
					fmt.Printf("Restored '%s'\n", args[0])
				}
			}
			return err
		},
	}
	h.rootCmd.AddCommand(restoreCmd)

	// show_trash
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "show_trash",
		Short: "List entries currently present in trash",
		Long:  "Lists the contents of the user's .trash directory without navigating into it.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := h.cacheHandler.ShowTrash()
			if err != nil {
				return err
			}
			if len(files) == 0 {
				fmt.Println("(trash is empty)")
				return nil
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
			return nil
		},
	})

	// clear_trash
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "clear_trash",
		Short: "Permanently delete all entries from trash",
		Long:  "Empties the user's .trash by permanently deleting all files and directories currently present.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			deleted, err := h.cacheHandler.ClearTrash()
			if err != nil {
				return err
			}
			if deleted == 0 {
				fmt.Println("Trash is already empty")
				return nil
			}
			fmt.Printf("Permanently deleted %d entr(y/ies) from trash\n", deleted)
			return nil
		},
	})

	// info
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "info",
		Short: "Show current directory information",
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := h.cacheHandler.GetFileInfo()
			if err != nil {
				return err
			}
			fmt.Printf("Directory Information:\n")
			fmt.Printf("  Name: %s\n", info.Name)
			fmt.Printf("  Type: %s\n", info.Type)
			fmt.Printf("  Size: %d bytes\n", info.Size)
			fmt.Printf("  FID:  %s\n", info.FID.String())
			return nil
		},
	})

	// clear
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Clear the terminal screen",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Print("\033[H\033[2J")
		},
	})

	// exit
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "exit",
		Short: "Exit the client",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Goodbye!")
			h.cacheHandler.ClearCache()
			os.Exit(0)
		},
	})

	// visualize cache
	h.rootCmd.AddCommand(&cobra.Command{
		Use:   "viscache",
		Short: "Visualize the current cache structure",
		Run: func(cmd *cobra.Command, args []string) {
			h.cacheHandler.VisualizeCache("")
		},
	})
}

// Start begins the interactive command loop
func (h *CobraHandler) Start() bool {
	completer := &CobraCompleter{handler: h}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "\033[32mdvfs>\033[0m ",
		HistoryFile:     os.TempDir() + "/dvfs_history.tmp",
		AutoComplete:    completer,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		fmt.Printf("Error initializing readline: %v", err)
		return false
	}
	defer rl.Close()
	h.rl = rl

	// Route all notification output through readline's stdout so the prompt
	// is correctly redrawn after each background notification message.
	h.cacheHandler.client.SetNotifyWriter(rl.Stdout())
	defer h.cacheHandler.client.SetNotifyWriter(nil)

	fmt.Println("=== Distributed VFS Client ===")
	fmt.Println("Type 'help' for available commands")
	fmt.Println()

	for {
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				if len(line) == 0 {
					return false // Exit program
				} else {
					continue
				}
			} else if err == io.EOF {
				return false // Exit program
			}
			fmt.Printf("Error reading input: %v", err)
			continue
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Use Cobra to execute the command
		h.rootCmd.SetArgs(strings.Fields(line))
		if err := h.rootCmd.Execute(); err != nil {
			// Check if this is the special return to metaserver error
			if err.Error() == "RETURN_TO_METASERVER" {
				return true // Return to metaserver selection
			}
			// Print other errors
			fmt.Printf("Error: %v\n", err)
		}
	}
}

// CobraCompleter implements readline.Completer using the Cobra command tree
type CobraCompleter struct {
	handler *CobraHandler
}

func (c *CobraCompleter) Do(line []rune, pos int) (newLine [][]rune, length int) {
	lineStr := string(line[:pos])
	parts := strings.Fields(lineStr)

	// If typing the first word (command)
	if len(parts) == 0 || (len(parts) == 1 && !strings.HasSuffix(lineStr, " ")) {
		prefix := ""
		if len(parts) == 1 {
			prefix = parts[0]
		}

		var suggestions [][]rune
		for _, cmd := range c.handler.rootCmd.Commands() {
			if strings.HasPrefix(cmd.Name(), prefix) {
				suggestions = append(suggestions, []rune(cmd.Name()[len(prefix):]))
			}
		}
		return suggestions, len(prefix)
	}

	// If typing arguments for a command
	commandName := parts[0]
	var targetCmd *cobra.Command
	for _, cmd := range c.handler.rootCmd.Commands() {
		if cmd.Name() == commandName {
			targetCmd = cmd
			break
		}
	}

	if targetCmd == nil {
		return nil, 0
	}

	// Commands that need remote file completion
	needsRemoteFile := map[string]bool{
		"cd":   true,
		"read": true,
		// "write":    true,
		"download": true,
		"delete":   true,
		"trash":    true,
		"restore":  true,
	}

	if needsRemoteFile[commandName] {
		prefix := ""
		if !strings.HasSuffix(lineStr, " ") && len(parts) > 1 {
			prefix = parts[len(parts)-1]
		}

		var (
			files []*FileInfo
			err   error
		)
		if commandName == "restore" {
			files, err = c.handler.cacheHandler.ShowTrash()
		} else {
			files, err = c.handler.cacheHandler.ListFiles()
		}
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
