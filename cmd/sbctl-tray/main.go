package main

import (
	"github.com/energye/systray"
)

func main() {
	systray.Run(onReady, onExit)
}

func onExit() {
	// Cleanup if needed.
}
