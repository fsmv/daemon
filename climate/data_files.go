package main

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
            err := dataFile.Close()
            if err != nil { // could be a write error on flush
                log.Printf("Failed to close data file: %v", err)
                // Still try to open the next file
            }
        }
        currDateStr := fmt.Sprintf("%04d-%02d-%02d", now.Year(), now.Month(), now.Day())
        file.Handle, err := os.OpenFile(filepath.Join(file.DataDir, currDateStr),
            os.O_RDWR|os.O_APPEND|os.O_CREATE, os.FileMode(0644))
        if err != nil {
            return err
        }
    }
    // Log the temperature
    return file.Handle.WriteString(fmt.Sprintf("%02d:%02d:%02d, %v\n",
        now.Hour(), now.Minute(), now.Second(), temperature))
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

// Assumes the lists are sorted. Inputs are unaffected, returns a copy.
func union(a, b []string) []string {
    var ret []string
    var last string
    take := func(arr []string) []string {
        // Skip duplicates
        if arr[0] == last {
            return arr[1:]
        }
        last = arr[0]
        // Save and move to the next one
        ret = append(ret, arr[0])
        return arr[1:]
    }
    for len(a) > 0 || len(b) > 0 {
        if len(a) == 0 {
            b = take(b)
        } else if len(b) == 0 {
            a = take(a)
        } else if a[0] < b[0] {
            a = take(a)
        } else if b[0] < a[0] {
            b = take(b)
        } else /* if a[0] == b[0] */ {
            a = take(a)
            b = b[1:] // skip the duplicate
        }
    }
    return ret
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
    // TODO: check for folders and recurse. The returned list should be the
    // union of all the dates in all the folders
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
        return ret[i] > ret[j]
    })
    return ret, nil
}

// Finds the newest data file and the one immediately before it.
//
//   - If err != nil then the data directory was not listable.
//   - If there are no data files, then both prev and newest will be the empty
//     string.
//   - If there is only one data file, then prev will be the empty string.
func findNewestDataDate(dataDir string) (prev, newest string, err error) {
    dates, err := listDataDates(dataDir)
    if err != nil || len(dates) == 0 {
        return
    }
    newest = dates[0]
    if len(dates) >= 2 {
        prev = dates[1]
    }
    return
}

// Finds the two data files immediately before and after the mid date.
//
//   - If there are no adjacent files in a direction, the empty string is
//     returned.
//   - If err != nil then the data directory was not listable.
//   - If mid does not exist then ("", "", nil) is returned.
func findAdjacentDataDates(dataDir, mid string) (prev, next string, err error) {
    dates, err := listDataDates(dataDir)
    if err != nil {
        return
    }
    var lastDate string
    for _, date := range dates {
        if date == mid {
            next = lastDate
        } else if lastDate == mid {
            prev = date
            break
        }
        lastDate = date
    }
    return
}
