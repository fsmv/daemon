package main

import (
    "errors"
    "os"
    "os/signal"
    "fmt"
    "flag"
    "log"
    "strings"
    "path/filepath"
    "time"
    "net/http"
    "strconv"

    "daemon/feproxy/proxyserv"
)

var (
    feproxyAddr = flag.String("feproxy_addr", "127.0.0.1:2048",
        "Address and port for the feproxy server")
    rootDataDir = flag.String("data_dir", "data",
        "Directory to save the daily temperature log files in")
    sensorReadInterval = flag.Duration("sensor_read_interval", 1 * time.Minute,
        "How often to read temperature from the sensor")
    testWebsiteOnly = flag.Bool("test_website_only", false,
        "If true, register under /climate-test and don't run the temperature logging code.")
)

const (
    registerProd = "/climate/"
    registerTest = "/climate-test/"

    indoorDataDir = "indoor"
    outdoorDataDir = "outdoor"
)

func promptForIntLessThan(max int) int {
    var ret int
    fmt.Scanln(&ret)
    for ret >= max {
        fmt.Println("Out of range, try again (-1 to stop): ")
        fmt.Scanln(&ret)
    }
    return ret
}

// TODO: split the sensor file reading from the prompting. Put it in measurement.go
func promptForSensorFiles() (indoor, outdoor *os.File, err error) {
    // List the device files
    devicesDir, err := os.Open(w1Dir)
    if err != nil {
        return nil, nil, err
    }
    names, err := devicesDir.Readdirnames(-1)
    devicesDir.Close()
    if err != nil {
        return nil, nil, err
    }
    // Open each sensor file and read the temperature
    type Device struct{
        file *os.File
        temperature float32
    }
    var devices []Device
    for _, name := range names {
        if strings.HasPrefix(name, w1MasterFilename) {
            continue
        }
        sensorFilename := filepath.Join(w1Dir, name, w1SensorFilename)
        sensorFile, err := os.Open(sensorFilename)
        if err != nil {
            return nil, nil, err
        }
        temperature, err := readTemperature(sensorFile)
        if err != nil {
            return nil, nil, fmt.Errorf(
                "Failed to read sensor file %#v: %v", sensorFilename, err)
        }
        devices = append(devices, Device{
            file:        sensorFile,
            temperature: temperature,
        })
    }
    // Prompt the user for the indoor and outdoor sensor indexes
    for i, d := range devices {
        fmt.Printf("%v. temperature reading: %v C\n", i, d.temperature)
    }
    fmt.Printf("Enter index of indoor sensor: ")
    indoorIdx := promptForIntLessThan(len(devices))
    fmt.Printf("Enter index of outdoor sensor: ")
    outdoorIdx := promptForIntLessThan(len(devices))
    // Close any un-used opened files (the gc would do it, but I can't help it)
    for i, d := range devices {
        if i == indoorIdx || i == outdoorIdx {
            continue
        }
        d.file.Close()
    }
    if indoorIdx == -1 || outdoorIdx == -1 {
        return nil, nil, errors.New(
            "Failed to select indoor and outdoor sensor files")
    }
    return devices[indoorIdx].file, devices[outdoorIdx].file, nil
}

func main() {
    flag.Parse()
    sigs := make(chan os.Signal, 2)
    signal.Notify(sigs, os.Interrupt, os.Kill)

    register := registerProd
    if *testWebsiteOnly {
        register = registerTest
    }
    fe, lease := proxyserv.MustConnectAndRegister(*feproxyAddr, register)
    defer fe.Unregister(lease.Pattern)
    defer fe.Close()
    go fe.MustKeepLeaseRenewedForever(lease)

    go func() {
        <-sigs
        log.Print("Shutting down...")
        // TODO(1.8): use http.Server and call close here
        os.Exit(0)
    }()

    if !*testWebsiteOnly {
        indoorFile, outdoorFile, err := promptForSensorFiles()
        if err != nil {
            log.Fatal(err)
        }
        go logTemperature(indoorFile, filepath.Join(*rootDataDir, indoorDataDir))
        go logTemperature(outdoorFile, filepath.Join(*rootDataDir, outdoorDataDir))
    }

    graphPage := GraphPageHandler{
        RootDataDir: *rootDataDir,
    }
    http.Handle(register, &graphPage)
    http.HandleFunc("/quit", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprint(w, "<body><h3>Shutting down!</h3></body>")
        time.AfterFunc(time.Second, func () {
            //client.Call("feproxy.Quit", struct{}{}, struct{}{})
            sigs <- os.Kill
        })
    })

    log.Print("Starting server...")
    // TODO(1.8): check for err == ErrServerClosed
    log.Fatal(http.ListenAndServe(":" + strconv.Itoa(int(lease.Port)), nil))
}
