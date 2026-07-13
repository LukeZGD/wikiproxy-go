package main

import (
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "net/url"
    "strings"
    "time"
    "os"
)

// --- Structs for API Responses ---

type SearchResponse struct {
    Query struct {
        SearchInfo struct {
            TotalHits int `json:"totalhits"`
        } `json:"searchinfo"`
        Search []struct {
            Title string `json:"title"`
        } `json:"search"`
    } `json:"query"`
}

type ParseResponse struct {
    Parse struct {
        Wikitext string `json:"wikitext"`
    } `json:"parse"`
}

// --- Structs for Output ---

type FirmwareKey struct {
    Image    string `json:"image"`
    Filename string `json:"filename"`
    Date     string `json:"date"`
    IV       string `json:"iv,omitempty"`
    Key      string `json:"key,omitempty"`
    KBAG     string `json:"kbag,omitempty"`
}

type FirmwareResponse struct {
    Identifier           string        `json:"identifier"`
    BuildID              string        `json:"buildid"`
    Codename             string        `json:"codename"`
    RestoreRamdiskExists bool          `json:"restoreramdiskexists"`
    UpdateRamdiskExists  bool          `json:"updateramdiskexists"`
    Keys                 []FirmwareKey `json:"keys"`
}

// --- Logic ---

// getKeyPage fetches the raw wikitext from the MediaWiki API.
func getKeyPage(identifier, buildid string) (string, error) {
    // 1. Search for the page
    searchURL := fmt.Sprintf(
        "https://theapplewiki.com/api.php?action=query&list=search&srsearch=%s&srlimit=1&format=json&srnamespace=2304",
        url.QueryEscape(fmt.Sprintf("intitle:%s %s", identifier, buildid)),
    )

    resp, err := http.Get(searchURL)
    if err != nil {
        return "", fmt.Errorf("search request failed: %w", err)
    }
    defer resp.Body.Close()

    var searchData SearchResponse
    if err := json.NewDecoder(resp.Body).Decode(&searchData); err != nil {
        return "", fmt.Errorf("failed to decode search response: %w", err)
    }

    if searchData.Query.SearchInfo.TotalHits == 0 || len(searchData.Query.Search) == 0 {
        return "", fmt.Errorf("no firmware keys page for device: %s, buildid: %s", identifier, buildid)
    }

    title := searchData.Query.Search[0].Title

    // 2. Fetch the wikitext for the found page
    parseURL := fmt.Sprintf(
        "https://theapplewiki.com/api.php?action=parse&prop=wikitext&page=%s&format=json&formatversion=2",
        url.QueryEscape(title),
    )

    resp2, err := http.Get(parseURL)
    if err != nil {
        return "", fmt.Errorf("parse request failed: %w", err)
    }
    defer resp2.Body.Close()

    var parseData ParseResponse
    if err := json.NewDecoder(resp2.Body).Decode(&parseData); err != nil {
        return "", fmt.Errorf("failed to decode parse response: %w", err)
    }

    return parseData.Parse.Wikitext, nil
}

// parseWikitext extracts the key-value pairs from the wiki template {{ ... }}
func parseWikitext(data string) map[string]string {
    pageData := make(map[string]string)

    start := strings.Index(data, "{{")
    end := strings.Index(data, "}}")

    if start != -1 && end != -1 && end > start {
        content := data[start+2 : end]
        parts := strings.Split(content, "|")

        for _, part := range parts {
            kv := strings.SplitN(part, "=", 2)
            if len(kv) == 2 {
                key := strings.TrimSpace(kv[0])
                val := strings.TrimSpace(kv[1])
                // Clean up wiki-specific formatting artifacts if any
                val = strings.TrimSpace(strings.ReplaceAll(val, "\n", ""))
                if key != "" {
                    pageData[key] = val
                }
            }
        }
    }
    return pageData
}

// parsePage builds the final JSON structure, mimicking the Python logic.
func parsePage(data, identifier, boardconfig string) (FirmwareResponse, error) {
    pageData := parseWikitext(data)

    // Boardconfig logic (handling Model2 replacements)
    if boardconfig != "" {
        if _, hasModel := pageData["Model"]; !hasModel {
            if _, hasModel2 := pageData["Model2"]; !hasModel2 {
                // Proceed if neither exist
            }
        } else {
            // Check if boardconfig exists in any of the values
            found := false
            target := strings.ToLower(boardconfig)
            for _, v := range pageData {
                if strings.ToLower(v) == target {
                    found = true
                    break
                }
            }
            if !found {
                return FirmwareResponse{}, fmt.Errorf("boardconfig: %s for device: %s is not valid", boardconfig, identifier)
            }

            // If Model2 matches the boardconfig, promote '2' keys to primary keys
            if strings.ToLower(pageData["Model2"]) == target {
                for k, v := range pageData {
                    if strings.Contains(k, "2") {
                        pageData[strings.ReplaceAll(k, "2", "")] = v
                    }
                }
            }

            // Delete all '2' keys
            for k := range pageData {
                if strings.Contains(k, "2") {
                    delete(pageData, k)
                }
            }
        }
    }

    _, hasRestoreRamdisk := pageData["RestoreRamdisk"]
    _, hasUpdateRamdisk := pageData["UpdateRamdisk"]

    response := FirmwareResponse{
        Identifier:           pageData["Device"],
        BuildID:              pageData["Build"],
        Codename:             pageData["Codename"],
        RestoreRamdiskExists: hasRestoreRamdisk,
        UpdateRamdiskExists:  hasUpdateRamdisk,
        Keys:                 []FirmwareKey{},
    }

    ignoredKeys := map[string]bool{
        "Version": true, "Build": true, "Device": true,
        "Model": true, "Codename": true, "Baseband": true, "DownloadURL": true,
    }

    for component, filename := range pageData {
        if ignoredKeys[component] {
            continue
        }
        if strings.HasSuffix(component, "Key") || strings.HasSuffix(component, "IV") || strings.HasSuffix(component, "KBAG") {
            continue
        }
        // Skip the template name itself usually at index 0 without an '='
        if component == "FirmwareKeys" || component == "Firmware keys" {
            continue
        }

        image := FirmwareKey{
            Image:    component,
            Filename: filename,
            Date:     time.Now().Format("2006-01-02T15:04:05.000000"),
        }

        if component == "RootFS" || component == "RestoreRamdisk" || component == "UpdateRamdisk" {
            image.Filename += ".dmg"
        }

        keysToCheck := []string{"IV", "Key"}
        if component == "RootFS" {
            keysToCheck = []string{"Key"}
        }

        for _, k := range keysToCheck {
            val, exists := pageData[component+k]
            if !exists {
                continue
            }
            lowerVal := strings.ToLower(val)
            if !strings.Contains(lowerVal, "unknown") && !strings.Contains(lowerVal, "not encrypted") {
                if k == "IV" {
                    image.IV = val
                } else if k == "Key" {
                    image.Key = val
                }
            }
        }

        if image.IV == "" && image.Key == "" && !strings.HasSuffix(image.Filename, ".dmg") {
            continue
        }

        if image.IV != "" && image.Key != "" {
            image.KBAG = image.IV + image.Key
        }

        response.Keys = append(response.Keys, image)
    }

    return response, nil
}

// --- Server & Handlers ---

func handleKeys(w http.ResponseWriter, r *http.Request) {
    // Go 1.14 method restriction check
    if r.Method != http.MethodGet {
        http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
        return
    }

    // Go 1.14 manual path segment parsing
    // r.URL.Path will look like "/firmware/iPhone4,1/11A4372q"
    // or "/firmware/iPhone4,1/n94ap/11A4372q"
    path := strings.Trim(r.URL.Path, "/")
    segments := strings.Split(path, "/")

    var identifier, boardconfig, buildid string

    // segments[0] is always "firmware" due to Mux routing
    switch len(segments) {
        case 3:
            // Format: /firmware/{identifier}/{buildid}
            identifier = segments[1]
            buildid = segments[2]
        case 4:
            // Format: /firmware/{identifier}/{boardconfig}/{buildid}
            identifier = segments[1]
            boardconfig = segments[2]
            buildid = segments[3]
        default:
            http.Error(w, "Not Found", http.StatusNotFound)
            return
    }

    wikitext, err := getKeyPage(identifier, buildid)
    if err != nil {
        http.Error(w, err.Error(), http.StatusNotFound)
        return
    }

    response, err := parsePage(wikitext, identifier, boardconfig)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(response)
}

func main() {
    mux := http.NewServeMux()

    // Go 1.14 compliant catch-all prefix route
    mux.HandleFunc("/firmware/", handleKeys)

    // Default to 8888
    port := "8888"

    // Override with command-line argument if provided
    if len(os.Args) > 1 {
        port = os.Args[1]
    }

    // Ensure the port string starts with a colon
    if !strings.HasPrefix(port, ":") {
        port = ":" + port
    }

    fmt.Printf("Server starting on http://127.0.0.1%s...\n", port)
    if err := http.ListenAndServe(port, mux); err != nil {
        log.Fatalf("Server failed to start: %v", err)
    }
}
