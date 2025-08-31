package main_test

import (
	"fmt"
	"net"
	"os"
	"testing"
)

func FreePort(t *testing.T) (int, net.Listener, *os.File) {
	t.Helper()
	// bind to localhost because we don't want to allow external connections
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal("Failed to listen on a free port:", err)
	}
	t.Cleanup(func() {
		l.Close()
	})
	return l.Addr().(*net.TCPAddr).Port, l, nil
}

func PortalPorts(t *testing.T) (*PortalTest, []string) {
	httpPort, hl, _ := FreePort(t)
	httpsPort, sl, _ := FreePort(t)
	rpcPort, rl, _ := FreePort(t)

	hl.Close()
	sl.Close()
	rl.Close()

	return &PortalTest{
			HTTPPort:  httpPort,
			HTTPSPort: httpsPort,
			RPCPort:   rpcPort,
		}, []string{
			fmt.Sprintf("-http_port=%v", httpPort),
			fmt.Sprintf("-https_port=%v", httpsPort),
			fmt.Sprintf("-rpc_port=%v", rpcPort),
		}

}
