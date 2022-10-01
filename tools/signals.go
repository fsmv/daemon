package tools

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

// Closes the given channel when the OS sends a signal to stop.
// Also logs which signal was received
//
// Catches: SIGINT, SIGKILL, SIGTERM, SIGHUP
func CloseOnQuitSignals(quit chan struct{}) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, os.Kill, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		switch <-sigs {
		case os.Interrupt:
			log.Print("Received keyboard interrupt")
		case os.Kill:
			log.Print("Received kill signal")
		case syscall.SIGTERM:
			log.Print("Received term signal")
		case syscall.SIGHUP:
			log.Print("Received hang up signal (parent process died)")
		}
		close(quit)
		signal.Stop(sigs)
		close(sigs)
	}()
}
