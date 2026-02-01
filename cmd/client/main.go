package main

import (
	"fmt"
	"log"
	"os"

	"github.com/umangshikarvar/dvfs/internal/client"
)

func main() {
	// Client configuration
	username := "romit"
	// Parse command line args
	if len(os.Args) > 1 {
		username = os.Args[1]
	}
	serverAddress := "127.0.0.1:50051"

	// Create and connect client
	c := client.NewClient(username)
	
	fmt.Printf("Connecting to server at %s as user %s...\n", serverAddress, username)
	
	if err := c.Connect(serverAddress); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	
	fmt.Printf("Connected successfully!\n\n")
	
	// Start interactive command handler
	handler := client.NewCommandHandler(c)
	handler.Start()
}