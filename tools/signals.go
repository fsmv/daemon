package tools

import (
  "log"
  "os"
  "os/signal"
  "syscall"
)

func CloseOnSignals(quit chan struct{}) {
  sigs := make(chan os.Signal, 2)
  signal.Notify(sigs, os.Interrupt, os.Kill, syscall.SIGTERM)
  go func() {
    switch <-sigs {
    case os.Interrupt:
      log.Print("Recieved keyboard interrupt")
    case os.Kill:
      log.Print("Recieved kill signal")
    case syscall.SIGTERM:
      log.Print("Recieved term signal")
    }
    close(quit)
  }()
}
