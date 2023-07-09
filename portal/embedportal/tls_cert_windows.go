package embedportal

import "os"

// Windows doesn't support sending signals so just make a dummy channel
func (t *tlsRefresher) refreshSignal() chan os.Signal {
	sig := make(chan os.Signal, 0)

	go func() {
		<-t.quit
		close(sig)
	}()

	return sig
}
