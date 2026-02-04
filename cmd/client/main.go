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
	flag.Parse()

	serverAddress := *ip_addr + ":50051"

	// Create and connect client
	c := client.NewClient(*username)

	fmt.Printf("Connecting to server at %s as user %s...\n", serverAddress, *username)

	if err := c.Connect(serverAddress); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}

	fmt.Printf("Connected successfully!\n\n")

	// Start interactive command handler
	handler := client.NewCommandHandler(c)
	handler.Start()
}
