package main

import (
    "os"
    "fmt"
    "log"
    "strconv"
    "time"
)

const (
    // One degree (0.85 actually) below absolute zero
    TemperatureErrVal = -274.0

    w1Dir = "/sys/bus/w1/devices/"
    w1MasterFilename = "w1_bus_master"
    w1SensorFilename = "w1_slave"
    // This is used for 12 bit output mode. I could check the whole message from
    // the sensor to check the mode, but I think the kernel always uses 12 bit.
    w1BitMultiplier = 0.0625
)

// Returns the temperature read from the w1 file in Celsius
// On error, returns TemperatureErrVal
func readTemperature(sensorFile *os.File) (float32, error) {
    // Read the data
    sensorFile.Sync()
    data := make([]byte, 5)
    _, err := sensorFile.ReadAt(data, /*offset=*/0)
    if err != nil {
        return TemperatureErrVal,
            fmt.Errorf("Failed to read file: %v", err)
    }
    // Parse the data
    if data[2] != ' ' {
        return TemperatureErrVal,
            fmt.Errorf("Invalid file format: %#v", string(data))
    }
    hexStr := string(append(data[3:5], data[0], data[1]))
    reading, err := strconv.ParseInt(hexStr, /*base=*/16, /*bits=*/16)
    if err != nil {
        return TemperatureErrVal,
            fmt.Errorf("Invalid file format: %#v", string(data))
    }
    return float32(reading) * w1BitMultiplier, nil;
}

func logTemperature(quit <-chan struct{}, sensor *os.File, dataDir string, readInterval time.Duration) {
    file := TemperatureFile{
        DataDir: dataDir,
    }
    defer func() {
        if err := file.Handle.Close(); err != nil {
            log.Printf("Failed to close final data file: %v", err)
        } else {
            log.Printf("Temperature logging for %#v safely stopped.", dataDir)
        }
    }()
    timer := time.NewTimer(readInterval)
    for {
        select {
        case <-quit:
            return
        case <-timer.C:
            timer.Reset(readInterval)
        }
        now := time.Now()
        temperature, err := readTemperature(sensor)
        if err != nil {
            maybeAlert("Error reading temperature", err, now)
            continue
        }
        if err := logTempMeasurement(&file, now, temperature); err != nil {
            maybeAlert("Error writing temperature", err, now)
        }
    }
}
