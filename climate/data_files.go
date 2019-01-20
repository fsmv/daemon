package main

import (
    "os"
    "fmt"
    "log"
    "time"
    "regexp"
    "strconv"
    "bufio"
    "math"
    "sort"
    "path/filepath"
)

var (
    // Used for matching filenames
    dateRegex = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
)

// Current file handle and information needed to switch to the next day file
// when it is time
type TemperatureFile struct {
    DataDir string
    Handle *os.File // Must close this when done, starts nil
    day int
}

// Expects to be called at least twice a month (only checks the day number to
// switch to the new file)
func logTempMeasurement(file *TemperatureFile, now time.Time, celsius float32) error {
    // Make sure the correct file is open for the day
    if file.day != now.Day() { // Day() is never 0, which is the default value
        file.day = now.Day()
        if file.Handle == nil {
            if err := os.MkdirAll(file.DataDir, os.FileMode(0775)); err != nil {
                return fmt.Errorf("Failed to make data directory %#v: %v",
                    file.DataDir, err)
            }
        } else {
            if err := file.Handle.Close(); err != nil {
                // could be a write error on flush
                // TODO: want to return this so we can alert, but still also try
                // to write the next value...
                log.Printf("Failed to close data file: %v", err)
                // Still try to open the next file
            }
        }
        var err error
        currDateStr := fmt.Sprintf("%04d-%02d-%02d", now.Year(), now.Month(), now.Day())
        file.Handle, err = os.OpenFile(filepath.Join(file.DataDir, currDateStr),
            os.O_RDWR|os.O_APPEND|os.O_CREATE, os.FileMode(0644))
        if err != nil {
            return err
        }
    }
    // Log the temperature
    _, err := file.Handle.WriteString(fmt.Sprintf("%02d:%02d:%02d, %v\n",
        now.Hour(), now.Minute(), now.Second(), celsius))
    return err
}

type TemperatureReading struct {
    Time    time.Time
    Celsius float32
}

func parseTempLine(line string) (TemperatureReading, error) {
    var ret TemperatureReading
    var timeStr, tempStr string
    for i := 0; i < len(line); i++ {
        if line[i] != ',' {
            if i == len(line)-1 {
                return ret, fmt.Errorf("expected comma")
            }
            continue
        }
        timeStr = line[:i]
        tempStr = line[i+2:]
        break
    }
    var err error
    ret.Time, err = time.Parse("15:04:05", timeStr)
    if err != nil {
        return ret, fmt.Errorf("failed to parse time: %v", err)
    }
    temp64, err := strconv.ParseFloat(tempStr, 32)
    ret.Celsius = float32(temp64)
    if err != nil {
        return ret, fmt.Errorf("failed to parse temperature: %v", err)
    }
    return ret, nil
}

func readTempLog(filename string) ([]TemperatureReading, error) {
    f, err := os.Open(filename)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    sc := bufio.NewScanner(f)
    var ret []TemperatureReading
    for sc.Scan() {
        temp, err := parseTempLine(sc.Text())
        if err != nil {
            return nil, err
        }
        ret = append(ret, temp)
    }
    if err := sc.Err(); err != nil {
        return nil, err
    }
    return ret, nil
}

func findTempRange(data []TemperatureReading) (min float32, max float32) {
    min, max = math.MaxFloat32, -math.MaxFloat32
    for _, r := range data {
        if r.Celsius < min {
            min = r.Celsius
        }
        if r.Celsius > max {
            max = r.Celsius
        }
    }
    return
}

// Returns a list of the data filenames (if the data dir is listable).
// All names are in the form yyyy-mm-dd and are sorted newest first.
func listDataDates(dataDir string) ([]string, error) {
    f, err := os.Open(dataDir)
    if err != nil {
        return nil, err
    }
    dataFiles, err := f.Readdir(-1)
    if err != nil {
        return nil, err
    }
    var ret []string
    for _, dataFile := range dataFiles {
        filename := dataFile.Name()
        if !dateRegex.MatchString(filename) {
            continue
        }
        ret = append(ret, filename)
    }
    // sort.Strings sorts oldest first :(
    sort.Slice(ret, func(i, j int) bool {
        // yyyy-mm-dd makes this easy
        return ret[i] > ret[j]
    })
    return ret, nil
}

type DataFolder struct {
    Folder string
    Dates []string
}

func ListDataFolders(rootDataDir string) ([]DataFolder, error) {
    f, err := os.Open(rootDataDir)
    if err != nil {
        return nil, fmt.Errorf(
            "Failed to open root data dir (%#v): %v", rootDataDir, err)
    }
    rootDirList, err := f.Readdir(-1)
    if err != nil {
        return nil, fmt.Errorf(
            "Failed to list root dir (%#v): %v", rootDataDir, err)
    }
    var ret []DataFolder
    for _, maybeDataFolder := range rootDirList {
        if !maybeDataFolder.IsDir() {
            continue
        }
        folder := maybeDataFolder.Name()
        dataDates, err := listDataDates(filepath.Join(rootDataDir, folder))
        if err != nil {
            // Maybe this should just log, it's fine this way for me though
            return nil, fmt.Errorf(
                "Failed to list data dates for %#v: %v", folder, err)
        }
        ret = append(ret, DataFolder{
            Folder: folder,
            Dates: dataDates,
        })
    }
    sort.Slice(ret, func(i, j int) bool {
        return ret[i].Folder < ret[j].Folder
    })
    return ret, nil
}

// Finds the newest date for a data file in any of dataFolders lists. Then,
// advances the pointer to cut off that date in the folders that have it.
// Finally, returns the newest date and the list of folders that contained it.
func nextNewestDate(dataFolders []DataFolder) (string, []string) {
    var nextNewestDate string
    var folders []string
    for _, dataFolder := range dataFolders {
        if len(dataFolder.Dates) == 0 {
            continue
        }
        folderNextDate := dataFolder.Dates[0]
        if nextNewestDate == "" || folderNextDate < nextNewestDate {
            nextNewestDate = folderNextDate
        }
    }
    if nextNewestDate == "" {
        return "", nil
    }
    for i, _ := range dataFolders {
        if len(dataFolders[i].Dates) == 0 {
            continue
        }
        folderNextDate := dataFolders[i].Dates[0]
        if folderNextDate == nextNewestDate {
            folders = append(folders, dataFolders[i].Folder)
            dataFolders[i].Dates = dataFolders[i].Dates[1:]
        }
    }
    return nextNewestDate, folders
}

// Finds the newest data date and the date immediately before it. Also returns
// the list of folders that have data for the newest date.
//
//   - If err != nil then the data directory was not listable.
//   - If there are no data files, then both prev and newest will be the empty
//     string. newestFolders will be nil.
//   - If there is only one data date, then prev will be the empty string.
func FindNewestDataDate(rootDataDir string) (prev, newest string, newestFolders []string, err error) {
    dataFolders, err := ListDataFolders(rootDataDir)
    if err != nil {
        return
    }
    newest, newestFolders = nextNewestDate(dataFolders)
    prev, _ = nextNewestDate(dataFolders)
    return
}

// Finds the two data files immediately before and after the mid date. Also
// returns the list of folders that have data for the mid date.
//
//   - If there are no adjacent data dates in a direction, the empty string is
//     returned.
//   - If err != nil then the data directory was not listable.
//   - If mid does not exist then ("", "", nil, nil) is returned.
func FindAdjacentDataDates(rootDataDir, mid string) (prev, next string, midFolders []string, err error) {
    dataFolders, err := ListDataFolders(rootDataDir)
    if err != nil {
        return
    }
    var lastNewestDate string
    for {
        date, folders := nextNewestDate(dataFolders)
        if date == "" { // All folders are empty now
            break
        }
        if date == mid {
            next = lastNewestDate
            midFolders = folders
        } else if lastNewestDate == mid {
            prev = date
        }
        lastNewestDate = date
    }
    return
}
