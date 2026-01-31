package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/umangshikarvar/dvfs/client"
	pb "github.com/umangshikarvar/dvfs/proto/fileserver"
)

func main() {
	// Configuration
	clientID := "client1"
	username := "alice"
	callbackPort := 60001

	// Parse command line args
	if len(os.Args) > 1 {
		username = os.Args[1]
	}
	if len(os.Args) > 2 {
		clientID = os.Args[2]
	}

	// Create client
	c := client.NewClient(clientID, username, callbackPort)

	// Start callback server
	if err := c.StartCallbackServer(); err != nil {
		log.Fatalf("Failed to start callback server: %v", err)
	}
	defer c.StopCallbackServer()

	// Mount "mydrive" to file server at localhost:50051
	// Server will create user directory and return user's root FID
	if err := c.AddMount("mydrive", "fs1", "localhost:50051", nil); err != nil {
		log.Fatalf("Failed to mount file server: %v", err)
	}

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nShutting down client...")
		c.StopCallbackServer()
		os.Exit(0)
	}()

	fmt.Printf("VFS Client started (user: %s, client_id: %s)\n", username, clientID)
	fmt.Println("Type 'help' for available commands")
	fmt.Println()

	// Interactive shell
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		cmd := parts[0]

		switch cmd {
		case "help":
			printHelp()

		case "create":
			if len(parts) < 3 {
				fmt.Println("Usage: create <path> <name> [dir]")
				fmt.Println("  path: parent directory path, use \"\" or . for root")
				continue
			}
			path := parts[1]
			// Treat "" or . as empty path (root directory)
			if path == `""` || path == "." {
				path = ""
			}
			name := parts[2]
			isDir := len(parts) > 3 && parts[3] == "dir"

			fid, err := c.Create(path, name, isDir)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				displayPath := path
				if displayPath == "" {
					displayPath = "/"
				} else if !strings.HasPrefix(displayPath, "/") {
					displayPath = "/" + displayPath
				}
				fmt.Printf("Created: %s/%s (FID: %s_%d_%d)\n", displayPath, name, fid.FileServerId, fid.InodeId, fid.GenerationNumber)
			}

		case "write":
			if len(parts) < 3 {
				fmt.Println("Usage: write <fid_string> <data>")
				continue
			}
			// Parse FID from string (simplified)
			fidStr := parts[1]
			data := strings.Join(parts[2:], " ")

			fid, err := parseFID(fidStr)
			if err != nil {
				fmt.Printf("Error parsing FID: %v\n", err)
				continue
			}

			// Open file
			fd, err := c.Open(fid, "w")
			if err != nil {
				fmt.Printf("Error opening file: %v\n", err)
				continue
			}

			// Write data
			n, err := c.Write(fd, []byte(data))
			if err != nil {
				fmt.Printf("Error writing: %v\n", err)
			} else {
				fmt.Printf("Wrote %d bytes\n", n)
			}

			// Close file
			c.Close(fd)

		case "read":
			if len(parts) < 2 {
				fmt.Println("Usage: read <fid_string>")
				continue
			}
			fidStr := parts[1]

			fid, err := parseFID(fidStr)
			if err != nil {
				fmt.Printf("Error parsing FID: %v\n", err)
				continue
			}

			// Open file
			fd, err := c.Open(fid, "r")
			if err != nil {
				fmt.Printf("Error opening file: %v\n", err)
				continue
			}

			// Read full file into cache
			if err := c.ReadFull(fd); err != nil {
				fmt.Printf("Error reading file: %v\n", err)
			}

			// Read from beginning
			c.Seek(fd, 0, 0)
			data, err := c.Read(fd, 10000) // read up to 10KB
			if err != nil {
				fmt.Printf("Error reading: %v\n", err)
			} else {
				fmt.Printf("Content:\n%s\n", string(data))
			}

			// Close file
			c.Close(fd)

		case "ls":
			if len(parts) < 2 {
				fmt.Println("Usage: ls <fid_string>")
				continue
			}
			fidStr := parts[1]

			fid, err := parseFID(fidStr)
			if err != nil {
				fmt.Printf("Error parsing FID: %v\n", err)
				continue
			}

			entries, err := c.ListDir(fid)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println("Directory contents:")
				for _, entry := range entries {
					typeStr := "FILE"
					if entry.Type == pb.InodeType_DIRECTORY {
						typeStr = "DIR "
					}
					fmt.Printf("  [%s] %s\n", typeStr, entry.Name)
				}
			}

		case "lookup":
			if len(parts) < 3 {
				fmt.Println("Usage: lookup <parent_fid_string> <name>")
				continue
			}
			fidStr := parts[1]
			name := parts[2]

			parentFID, err := parseFID(fidStr)
			if err != nil {
				fmt.Printf("Error parsing FID: %v\n", err)
				continue
			}

			fid, err := c.Lookup(parentFID, name)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Printf("Found: %s_%d_%d\n", fid.FileServerId, fid.InodeId, fid.GenerationNumber)
			}

		case "exit", "quit":
			fmt.Println("Goodbye!")
			return

		default:
			fmt.Printf("Unknown command: %s (type 'help' for available commands)\n", cmd)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error reading input: %v", err)
	}
}

func printHelp() {
	fmt.Println("Available commands:")
	fmt.Println("  create <path> <name> [dir]          - Create a file or directory")
	fmt.Println("  write <fid> <data>                  - Write data to a file")
	fmt.Println("  read <fid>                          - Read and display file contents")
	fmt.Println("  ls <fid>                            - List directory contents")
	fmt.Println("  lookup <parent_fid> <name>          - Lookup a file in directory")
	fmt.Println("  exit/quit                           - Exit the client")
	fmt.Println()
	fmt.Println("FID format: serverid_inodeid_generation (e.g., fs1_0_1 for root)")
}

func parseFID(fidStr string) (*pb.FID, error) {
	parts := strings.Split(fidStr, "_")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid FID format, expected serverid_inodeid_generation")
	}

	var inodeID, generation uint64
	_, err := fmt.Sscanf(parts[1]+"_"+parts[2], "%d_%d", &inodeID, &generation)
	if err != nil {
		return nil, fmt.Errorf("invalid FID format: %v", err)
	}

	return &pb.FID{
		FileServerId:     parts[0],
		InodeId:          inodeID,
		GenerationNumber: generation,
	}, nil
}
