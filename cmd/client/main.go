package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/umangshikarvar/dvfs/internal/client"
)

func main() {
	// Client configuration
	username := flag.String("username", "romit", "enter username")
	root_user := flag.String("root_user", "root", "enter root username for access control")
	ip_addr := flag.String("ip_addr", "127.0.0.1", "enter ip_addr for mds/fs")
	metaserver := flag.Bool("meta", true, "to go via metaserver or not")
	port := flag.String("port", "", "enter port for mds/fs")
	useTLS := flag.Bool("tls", false, "Enable TLS (default: false)")

	flag.Parse()

	if *port == "" {
		if *metaserver {
			*port = "50051"
		} else {
			*port = "50052"
		}
	}

	// Create and connect client
	c := client.NewClient(*username, *root_user, *useTLS)

	serverAddress := fmt.Sprintf("%s:%s", *ip_addr, *port)

	if *metaserver {
		roots, err := c.GetRoots(*ip_addr + ":" + *port)
		if err != nil {
			log.Fatalf("Failed to get roots: %v", err)
		}

		if len(roots) == 0 {
			log.Fatalf("No roots available")
		}

		fmt.Println("\nAvailable roots:")
		fmt.Println("────────────────────────────────")

		fmt.Printf("  %-3s %-15s %-10s\n", "#", "ROOT", "OWNER")
		fmt.Println("  --------------------------------")

		for i, root := range roots {
			owner := root.Owner
			if owner == *username {
				owner = "you"
			}

			fmt.Printf("  %-3d %-15s %-10s\n", i+1, root.DisplayName, owner)
		}

		fmt.Println("\n  0   Exit")

		var selectedRootUser string

		for {
			var choice int

			fmt.Printf("\nSelect root [1-%d] or 0 to exit: ", len(roots))

			_, err := fmt.Scanln(&choice)
			if err != nil {
				fmt.Println("Invalid input. Enter a number.")
				continue
			}

			if choice == 0 {
				fmt.Println("Goodbye!")
				return
			}

			if choice >= 1 && choice <= len(roots) {
				selectedRootUser = roots[choice-1].Owner
				c.SetRootPath(roots[choice-1].DisplayName, roots[choice-1].Path)
				break
			}

			fmt.Println("Invalid selection. Try again.")
		}

		if selectedRootUser == "mydrive" {
			selectedRootUser = *username
		}

		c.SetRootUser(selectedRootUser)

		fileserver, err := c.NavigateToFileServer(*ip_addr + ":" + *port)
		if err != nil {
			log.Fatalf("Failed to navigate to file server: %v", err)
		}

		serverAddress = fileserver
	}

	fmt.Printf("Connecting to server at %s as user %s...\n", serverAddress, *username)

	fid, err := c.Connect(serverAddress)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	} else {
		fmt.Printf("Connected successfully! Root FID: %s\n\n", fid.String())
	}

	fmt.Printf("Connected successfully!\n\n")

	cacheHandler := client.NewCacheHandler(c, fid) // initialise and populate cache handler with root directory and its contents from server
	if cacheHandler == nil {
		log.Fatalf("Failed to initialize cache handler")
	}
	cacheHandler.VisualizeCache("") // visualize the cache structure after initialization

	// Start interactive command handler
	handler := client.NewCobraHandler(cacheHandler)
	handler.Start()
}
