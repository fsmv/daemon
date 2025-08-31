//go:build unix

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
	f, err := l.File()
	if err != nil {
		t.Fatal("Failed to get file for a free port:", err)
	}
	t.Cleanup(func() {
		l.Close()
		f.Close()
	})
	return l.Addr().(*net.TCPAddr).Port, l, f
}

func PortalPorts(t *testing.T) (*PortalTest, []string) {
	httpPort, _, httpFD := FreePort(t)
	httpsPort, _, httpsFD := FreePort(t)
	rpcPort, _, rpcFD := FreePort(t)

	return &PortalTest{
			HTTPPort:  httpPort,
			HTTPSPort: httpsPort,
			RPCPort:   rpcPort,
		}, []string{
			fmt.Sprintf("-http_port=-%v", httpFD.Fd()),
			fmt.Sprintf("-https_port=-%v", httpsFD.Fd()),
			fmt.Sprintf("-rpc_port=-%v", rpcFD.Fd()),
		}
}
