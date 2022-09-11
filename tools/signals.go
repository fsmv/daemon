package tools

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

func CloseOnQuitSignals(quit chan struct{}) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, os.Kill, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		switch <-sigs {
		case os.Interrupt:
			log.Print("Recieved keyboard interrupt")
		case os.Kill:
			log.Print("Recieved kill signal")
		case syscall.SIGTERM:
			log.Print("Recieved term signal")
		case syscall.SIGHUP:
			log.Print("Recieved hang up signal (parent process died)")
		}
		close(quit)
		signal.Stop(sigs)
		close(sigs)
	}()
}
