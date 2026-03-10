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
	ip_addr := flag.String("ip_addr", "127.0.0.1", "enter ip_addr")
	port := flag.String("port", "50051", "enter port")
	metaserver := flag.Bool("meta", true, "to go via metaserver or not")
	useTLS := flag.Bool("tls", false, "Enable TLS (default: false)")
	flag.Parse()

	// Create and connect client
	c := client.NewClient(*username, *useTLS)

	serverAddress := fmt.Sprintf("%s:%s", *ip_addr, *port)

	// If metaserver flag is set, navigate to the appropriate file server based on the username
	if *metaserver {
		fileserver, err := c.NavigateToFileServer(*ip_addr+":"+*port, *username)
		if err != nil {
			log.Fatalf("Failed to navigate to file server: %v", err)
		}
		serverAddress = fileserver
	}

	fmt.Printf("Connecting to server at %s as user %s...\n", serverAddress, *username)

	if err := c.Connect(serverAddress); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	fmt.Printf("Connected successfully!\n\n")

	// Start interactive command handler
	handler := client.NewCobraHandler(c)
	handler.Start()
}
