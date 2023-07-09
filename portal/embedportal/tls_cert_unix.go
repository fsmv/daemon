//go:build !windows

package embedportal

import (
	"os"
	"os/signal"
	"syscall"
)

func (t *tlsRefresher) refreshSignal() chan os.Signal {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGUSR1)

	go func() {
		<-t.quit
		signal.Stop(sig)
		close(sig)
	}()

	return sig
}
