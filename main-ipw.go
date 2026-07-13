package main

import (
    "encoding/json"
    "fmt"
    "io/ioutil"
    "log"
    "net/http"
    "net/url"
    "os"
    "regexp"
    "strings"
    "time"

    "github.com/PuerkitoBio/goquery"
)

// --- Structs for Output ---

type FirmwareKey struct {
    Image    string `json:"image"`
    Filename string `json:"filename"`
    Date     string `json:"date"`
    IV       string `json:"iv"`
    Key      string `json:"key"`
    KBAG     string `json:"kbag"`
}

type FirmwareResponse struct {
    Identifier           string        `json:"identifier"`
    BuildID              string        `json:"buildid"`
    Codename             string        `json:"codename"`
    UpdateRamdiskExists  bool          `json:"updateramdiskexists"`
    RestoreRamdiskExists bool          `json:"restoreramdiskexists"`
    Keys                 []FirmwareKey `json:"keys"`
}

const wiki = "https://www.theiphonewiki.com"

// getFirmwareKeysPage mimics the regex search to find the specific firmware page URL
func getFirmwareKeysPage(device, buildnum string) (string, error) {
    searchURL := fmt.Sprintf("%s/w/index.php?search=%s", wiki, url.QueryEscape(buildnum+" "+device))

    resp, err := http.Get(searchURL)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    bodyBytes, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        return "", err
    }
    html := string(bodyBytes)

    // Regex equivalent to: "\/wiki\/.*_"+buildnum+"_\("+device+"\)"
    reStr := fmt.Sprintf(`\/wiki\/[^\s"']*_%s_\(%s\)`, regexp.QuoteMeta(buildnum), regexp.QuoteMeta(device))
    re := regexp.MustCompile(reStr)

    match := re.FindString(html)
    if match == "" {
        return "", fmt.Errorf("could not find firmware page link in search results")
    }

    return wiki + match, nil
}

// getkeys fetches the page and parses it using goquery (equivalent to pyquery)
func getkeys(device, buildnum string) (FirmwareResponse, error) {
    rsp := FirmwareResponse{
        Identifier:           device,
        BuildID:              buildnum,
        UpdateRamdiskExists:  false,
        RestoreRamdiskExists: false,
        Keys:                 []FirmwareKey{},
    }

    pageLink, err := getFirmwareKeysPage(device, buildnum)
    if err != nil {
        return rsp, err
    }

    // Extract codename from the URL (e.g., InnsbruckVailPrime from ..._11A4372q_(iPhone4,1))
    parts := strings.Split(pageLink, "/")
    lastPart := parts[len(parts)-1]
    rsp.Codename = strings.Split(lastPart, "_")[0]

    resp, err := http.Get(pageLink)
    if err != nil {
        return rsp, err
    }
    defer resp.Body.Close()

    doc, err := goquery.NewDocumentFromReader(resp.Body)
    if err != nil {
        return rsp, err
    }

    doc.Find("span.mw-headline").Each(func(i int, span *goquery.Selection) {
        id, _ := span.Attr("id")
        if id == "Update_Ramdisk" {
            rsp.UpdateRamdiskExists = true
        }
        if id == "Restore_Ramdisk" {
            rsp.RestoreRamdiskExists = true
        }

        name := span.Text()
        if name == "Root Filesystem" {
            name = "RootFS"
        }

        // Find the filename. Python used: span.parent().next("* > span.keypage-filename")
        parent := span.Parent()
        fname := parent.NextAll().Find("span.keypage-filename").First().Text()

        nameCleaned := strings.ReplaceAll(name, " ", "")
        lowerName := strings.ToLower(nameCleaned)

        // Find IV, Key, and KBAG based on their unique IDs
        iv := doc.Find(fmt.Sprintf("code#keypage-%s-iv", lowerName)).First().Text()
        key_ := doc.Find(fmt.Sprintf("code#keypage-%s-key", lowerName)).First().Text()
        kbag := doc.Find(fmt.Sprintf("code#keypage-%s-kbag", lowerName)).First().Text()

        // Mimic the Python `try/except: continue` block
        // If these are empty, we assume they weren't found properly
        if iv == "" && key_ == "" && kbag == "" {
            return // continue to next iteration in Each() loop
        }

        keyObj := FirmwareKey{
            Image:    nameCleaned,
            Filename: fname, // WARNING: This is just the filename as noted in the python script
            Date:     time.Now().Format("2006-01-02T15:04:05.000000"), // ISO format matching python
            IV:       iv,
            Key:      key_,
            KBAG:     kbag,
        }

        rsp.Keys = append(rsp.Keys, keyObj)
    })

    return rsp, nil
}

// --- Server & Handlers ---

func handleKeys(w http.ResponseWriter, r *http.Request) {
    // Go 1.14 method check
    if r.Method != http.MethodGet {
        http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
        return
    }

    // Manual path segment parsing for Go 1.14
    path := strings.Trim(r.URL.Path, "/")
    segments := strings.Split(path, "/")

    if len(segments) != 3 || segments[0] != "firmware" {
        http.Error(w, "Not Found. Endpoint is /firmware/<device>/<buildid>", http.StatusNotFound)
        return
    }

    device := segments[1]
    buildid := segments[2]

    fmt.Printf("Getting keys for /%s/%s\n", device, buildid)

    response, err := getkeys(device, buildid)
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")

    // Print JSON to console like the python script does
    jsonBytes, _ := json.MarshalIndent(response, "", "  ")
    //fmt.Println(string(jsonBytes) + "\n")

    w.Write(jsonBytes)
}

func main() {
    mux := http.NewServeMux()
    mux.HandleFunc("/firmware/", handleKeys)

    // Default to 8888, allow override via CLI args
    port := "8888"
    if len(os.Args) > 1 {
        port = os.Args[1]
    }
    if !strings.HasPrefix(port, ":") {
        port = ":" + port
    }

    fmt.Println("running webserver")
    fmt.Printf("Listening on 0.0.0.0%s...\n", port)

    if err := http.ListenAndServe(port, mux); err != nil {
        log.Fatalf("Server failed to start: %v", err)
    }
}
