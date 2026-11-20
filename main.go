package main

import (
	"context"
	"os"

	"github.com/joho/godotenv"
	"github.com/warpstreamlabs/bento/public/service"

	// Import all standard Bento components (includes Kafka)
	_ "github.com/warpstreamlabs/bento/public/components/all"

	// Import custom Flexprice output plugin
	"github.com/flexprice/bento-collector/collector"
	_ "github.com/flexprice/bento-collector/output"
)

func main() {
	// Automatically load .env file if it exists
	// This allows environment variables to be loaded from .env file without
	// requiring users to manually run "source .env" before running the application
	//
	// The .env file is optional - if it doesn't exist, the application will
	// use environment variables from the shell or system environment
	// Errors are ignored since .env file is optional
	_ = godotenv.Load()
	if len(os.Args) > 1 && os.Args[1] == "collector" {
		os.Exit(collector.Main(os.Args[2:], os.Stdout, os.Stderr))
	}

	// Run Bento as a service with CLI support
	// This will read config from -c flag or stdin
	service.RunCLI(context.Background())
}
