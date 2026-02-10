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
	
	cacheHandler := client.NewCacheHandler(c)   // initialise and populate cache handler with root directory and its contents from server
	if cacheHandler == nil {
		log.Fatalf("Failed to initialize cache handler")
	}
	cacheHandler.VisualizeCache("") // visualize the cache structure after initialization

	// Start interactive command handler
	handler := client.NewCobraHandler(cacheHandler)
	handler.Start()
}
