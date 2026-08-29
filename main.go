package main

import (
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"

	"bubbles/internal/tui"
)

func main() {
	if _, err := os.Stat(".env"); err == nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("warning: could not load .env: %v", err)
		}
	}

	for _, a := range os.Args[1:] {
		if a == "-h" || a == "--help" || a == "help" {
			printUsage()
			return
		}
	}

	if err := tui.Run(); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func printUsage() {
	fmt.Println(`Bubbles - YouTube playlist viewer & analyzer

Usage:
  bubbles      Launch the interactive terminal UI

Simply run 'bubbles' and choose a playlist. You can browse its videos or
classify them into Party / Love / Workout / Chill / Sad / Other.`)
}
