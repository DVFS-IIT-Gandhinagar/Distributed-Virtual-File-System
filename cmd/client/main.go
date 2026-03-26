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
	
	fid, err := c.Connect(serverAddress)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	} else {
		fmt.Printf("Connected successfully! Root FID: %s\n\n", fid.String())
	}
	
	fmt.Printf("Connected successfully!\n\n")
	
	cacheHandler := client.NewCacheHandler(c, fid)   // initialise and populate cache handler with root directory and its contents from server
	if cacheHandler == nil {
		log.Fatalf("Failed to initialize cache handler")
	}
	cacheHandler.VisualizeCache("") // visualize the cache structure after initialization

	// Start interactive command handler
	handler := client.NewCobraHandler(cacheHandler)
	handler.Start()
}
