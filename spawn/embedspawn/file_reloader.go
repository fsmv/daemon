package embedspawn

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
)

type refreshFile struct {
	writePipe *os.File
	fileName  string
}

type fileRefresher []refreshFile

func (f fileRefresher) refreshOnSignal(quit chan struct{}) {
	sigs := make(chan os.Signal, 1)
	sigs <- syscall.SIGUSR1 // trigger the initial run (buffered)
	signal.Notify(sigs, syscall.SIGUSR1)
	// Close in a goroutine because we might block on writing to the pipe and we
	// need to close it asynchronously to unblock that this parent goroutine
	go func() {
		<-quit
		signal.Stop(sigs)
		close(sigs)
		for _, file := range f {
			file.writePipe.Close()
		}
	}()
	for {
		select {
		case <-quit:
			return
		case <-sigs:
			log.Print("Starting TLS certificate refresh...")
			errCount := 0
			for _, refresh := range f {
				dataFile, err := os.Open(refresh.fileName)
				if err != nil {
					log.Printf("Failed opening file for refresh %#v. You can send another SIGUSR1 to this binary to retry. Error message: %w", refresh.fileName, err)
					dataFile.Close()
					errCount++
					continue
				}
				if n, err := io.Copy(refresh.writePipe, dataFile); err != nil {
					log.Printf("Failed to refresh file on write to the OS pipe for %#v (wrote %v bytes): %w",
						refresh.fileName, n, err)
					errCount++
				}
				refresh.writePipe.WriteString("\x04") // EOT
				refresh.writePipe.Sync()
				dataFile.Close()
			}
			if errCount == 0 {
				log.Print("Successfully refreshed TLS certificate...")
			}
		}
	}
}

func startFileRefresh(files []string, quit chan struct{}) ([]*os.File, error) {
	var ret []*os.File

	var refresher fileRefresher
	for _, fileName := range files {
		// Test if we can open the file
		f, err := os.Open(fileName)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("error opening file %#v: %w", fileName, err)
		}

		r, w, err := os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create OS pipe to refresh file %#v: %w",
				fileName, err)
		}
		ret = append(ret, r)
		refresher = append(refresher, refreshFile{
			writePipe: w,
			fileName:  fileName,
		})
	}
	go refresher.refreshOnSignal(quit)
	log.Print("Started -auto_tls_certs pipe")
	return ret, nil
}
